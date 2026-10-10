package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// `devtool release-scope` is SP17-M7-07's "complete-or-unknown accounting" (coordinator ruling
// R7-2): for every release target and every acceptance row, what is established, what is not, and
// which committed artifact says so.
//
// The rule that makes it worth reading is that a status is only ever raised by a RECORD. Prose in
// an evidence page, a claim in a report and a green test log are all incapable of moving a row:
// the collectors below read JSON records with an `outcome` field, and everything else defaults to
// unverified/unknown. A release-scope that could be talked up by its own documentation would be a
// summary of the documentation, not of the evidence.
const (
	scopeEvidenceDefault = "plans/sdd/V6-SP-17-packaging-hardening-and-release"

	// The four target statuses, strongest first.
	scopeInstalled  = "installed-verified"
	scopeValidated  = "directory-validated"
	scopeBuilt      = "built"
	scopeUnknown    = "unknown"
	scopeVerified   = "verified"
	scopeUnverified = "unverified"
	scopeExcluded   = "excluded"
)

// scopeTargetRank orders the four target statuses so a collector can only ever raise one.
var scopeTargetRank = map[string]int{scopeUnknown: 0, scopeBuilt: 1, scopeValidated: 2, scopeInstalled: 3}

// scopeRecord is the shape every evidence record this tool reads shares. Task 2/3/4's matrices and
// Task 8's install records disagree about how a target is spelled — `target.os`/`target.arch`
// versus `target.platform` — so both are decoded and targetKey reconciles them. No field here is
// invented: each one appears in a record already committed under plans/sdd/, or in the interface
// contract Task 8 and this tool were both given (install-record-schema.md).
type scopeRecord struct {
	Name       string `json:"name"`
	Capability string `json:"capability"`
	Outcome    string `json:"outcome"`
	Reason     string `json:"reason"`
	Artifact   string `json:"artifact"`
	Target     struct {
		OS       string `json:"os"`
		Arch     string `json:"arch"`
		Platform string `json:"platform"`
	} `json:"target"`
	// file is where the record was read from, relative to the evidence root. It is the citation
	// printed beside every status and is never read from the record's own `artifact` field unless
	// that field is present, so a record cannot misattribute itself.
	file string
}

// targetKey is the `os/arch` this record is about, or "" when it names no target.
func (r scopeRecord) targetKey() string {
	if r.Target.Platform != "" {
		return r.Target.Platform
	}
	if r.Target.OS != "" && r.Target.Arch != "" {
		return r.Target.OS + "/" + r.Target.Arch
	}
	return ""
}

// citation is the artifact path a row cites for this record.
func (r scopeRecord) citation() string {
	if r.Artifact != "" {
		return r.Artifact
	}
	return r.file
}

// scopeEvidence is everything the collectors found, grouped by the directory family it came from.
type scopeEvidence struct {
	// Root is the evidence directory that was read, as the caller spelled it.
	Root string
	// ByFamily maps a record family (platform, security, fault, switches, install) to its records.
	ByFamily map[string][]scopeRecord
	// HostValidation is Task 1's single record, when one is present.
	HostValidation *scopeRecord
	// Files is the set of plain file names directly under Root, for the presence checks the
	// accounting rows make (a named artifact being attached is a file fact, not a claim).
	Files map[string]bool
	// ReleaseCheck is the committed release-check summary at the evidence root (release-check.json),
	// when one is present and decodable. Its outcome is derived from `ok` and the step statuses.
	ReleaseCheck *scopeReleaseCheck
}

// scopeReleaseCheck is dist/release-check.json's shape committed as release-check.json. The
// document has no `outcome` field; release-scope derives one (verified iff ok and no FAIL step).
type scopeReleaseCheck struct {
	file     string
	outcome  string
	failStep string
}

// scopeFamilies maps an evidence subdirectory prefix to the family its records belong to.
var scopeFamilies = map[string]string{
	"commit2-platform-": "platform",
	"commit3-security-": "security",
	"commit4-fault-":    "fault",
	"commit7-switches-": "switches",
	"commit8-install-":  "install",
}

// taskReleaseScope renders the scope tables.
func taskReleaseScope(args []string) error {
	fs := flag.NewFlagSet("release-scope", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "render the two tables as one JSON document")
	asMarkdown := fs.Bool("markdown", false, "render the two tables as Markdown (the default)")
	evidence := fs.String("evidence", scopeEvidenceDefault, "directory holding the committed evidence records")
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}
	if *asJSON && *asMarkdown {
		return errors.Join(errUsage, errors.New("release-scope: --json and --markdown are alternatives"))
	}

	// The default evidence directory is under plans/, which is maintainer-only and not published in
	// the repository. Without it every row is unknown/unverified, so say why instead of rendering
	// that silently.
	if *evidence == scopeEvidenceDefault {
		if _, err := os.Stat(rootRelative(*evidence)); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "release-scope: %s is maintainer-only evidence and is not in this "+
				"checkout; every row below is unknown/unverified\n", *evidence)
		}
	}

	ev, err := collectScopeEvidence(rootRelative(*evidence), *evidence)
	if err != nil {
		return err
	}
	report := buildScopeReport(ev)
	if *asJSON {
		b, err := marshalBundleJSON(report)
		if err != nil {
			return err
		}
		fmt.Print(string(b))
		return nil
	}
	fmt.Print(renderScopeMarkdown(report))
	return nil
}

// collectScopeEvidence reads dir. A missing directory is not an error: an empty evidence set is a
// legitimate answer (everything unknown/unverified), and failing instead would make the command
// unusable exactly when it is most needed — on a fresh checkout, before anything has been proven.
func collectScopeEvidence(dir, asSpelled string) (scopeEvidence, error) {
	ev := scopeEvidence{Root: asSpelled, ByFamily: map[string][]scopeRecord{}, Files: map[string]bool{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ev, nil
		}
		return ev, fmt.Errorf("release-scope: reading %s: %w", asSpelled, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			ev.Files[e.Name()] = true
			if e.Name() == "commit1-host-validation.json" {
				rec, errText, ok := readHostValidationRecord(filepath.Join(dir, e.Name()), e.Name())
				if ok {
					ev.HostValidation = &rec
				} else if errText != "" {
					fmt.Fprintf(os.Stderr, "release-scope: skipping %s: %s\n", e.Name(), errText)
				}
			}
			if e.Name() == "release-check.json" {
				rec, errText, ok := readReleaseCheckRecord(filepath.Join(dir, e.Name()), e.Name())
				if ok {
					ev.ReleaseCheck = &rec
				} else if errText != "" {
					fmt.Fprintf(os.Stderr, "release-scope: skipping %s: %s\n", e.Name(), errText)
				}
			}
			continue
		}
		family := ""
		for prefix, f := range scopeFamilies {
			if strings.HasPrefix(e.Name(), prefix) {
				family = f
			}
		}
		if family == "" {
			continue
		}
		recs, err := readScopeRecords(filepath.Join(dir, e.Name()), e.Name())
		if err != nil {
			return ev, err
		}
		ev.ByFamily[family] = append(ev.ByFamily[family], recs...)
	}
	for f := range ev.ByFamily {
		sort.Slice(ev.ByFamily[f], func(i, j int) bool { return ev.ByFamily[f][i].file < ev.ByFamily[f][j].file })
	}
	return ev, nil
}

// readScopeRecords reads every record file in one evidence subdirectory. INDEX.json is skipped:
// individual files are authoritative and INDEX.json is a convenience copy. Reading both would
// double-count every record.
func readScopeRecords(dir, rel string) ([]scopeRecord, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("release-scope: reading %s: %w", rel, err)
	}
	var out []scopeRecord
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == "INDEX.json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("release-scope: reading %s/%s: %w", rel, e.Name(), err)
		}
		var rec scopeRecord
		if err := json.Unmarshal(b, &rec); err != nil {
			// A record this tool cannot decode must not raise anything, and must not be silently
			// dropped either: it is reported and skipped, so the absence is visible.
			fmt.Fprintf(os.Stderr, "release-scope: skipping %s/%s: %v\n", rel, e.Name(), err)
			continue
		}
		if rec.Outcome == "" {
			fmt.Fprintf(os.Stderr, "release-scope: skipping %s/%s: record has no outcome\n", rel, e.Name())
			continue
		}
		rec.file = rel + "/" + e.Name()
		out = append(out, rec)
	}
	return out, nil
}

// readHostValidationRecord decodes Task 1's record into the common shape. Its target lives under
// `bundle.target` rather than `target`, and its accepted-outcome spelling is the one
// tools/devtool/bundlevalidate.go writes (`accepted`); the interface ruling calls the same state
// "validated", so both are honoured and nothing else is.
func readHostValidationRecord(path, rel string) (scopeRecord, string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return scopeRecord{}, err.Error(), false
	}
	var doc struct {
		Outcome string `json:"outcome"`
		Bundle  struct {
			Target struct {
				OS   string `json:"os"`
				Arch string `json:"arch"`
			} `json:"target"`
		} `json:"bundle"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return scopeRecord{}, err.Error(), false
	}
	rec := scopeRecord{Name: "host-validation", Capability: "host_validate", Outcome: doc.Outcome, file: rel}
	rec.Target.OS, rec.Target.Arch = doc.Bundle.Target.OS, doc.Bundle.Target.Arch
	return rec, "", rec.targetKey() != ""
}

// readReleaseCheckRecord decodes a committed release-check summary. outcome is verified iff
// ok == true and no step has status FAIL; otherwise failed (a FAIL pins the row).
func readReleaseCheckRecord(path, rel string) (scopeReleaseCheck, string, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return scopeReleaseCheck{}, err.Error(), false
	}
	var doc struct {
		OK    bool `json:"ok"`
		Steps []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return scopeReleaseCheck{}, err.Error(), false
	}
	rec := scopeReleaseCheck{file: rel, outcome: scopeVerified}
	for _, s := range doc.Steps {
		if s.Status == "FAIL" {
			rec.outcome = "failed"
			rec.failStep = s.Name
			break
		}
	}
	if !doc.OK {
		rec.outcome = "failed"
	}
	return rec, "", true
}
