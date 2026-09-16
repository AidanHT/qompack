package main

import (
	"fmt"
	"strings"
)

// scopeTargetRow is one release target's strongest established status.
type scopeTargetRow struct {
	Target   string `json:"target"`
	Status   string `json:"status"`
	Artifact string `json:"artifact,omitempty"`
}

// scopeAcceptanceRow is one SP17-M7 acceptance row's status.
type scopeAcceptanceRow struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Artifact string `json:"artifact,omitempty"`
	Note     string `json:"note,omitempty"`
}

// scopeReport is the whole accounting, in the order it is rendered.
type scopeReport struct {
	Evidence   string               `json:"evidence"`
	Targets    []scopeTargetRow     `json:"targets"`
	Acceptance []scopeAcceptanceRow `json:"acceptance"`
}

// acceptanceTitles is the eight rows of plans/V6-SP-17-…md §2, quoted short. The IDs are what the
// plan's acceptance table names; the prose beside each is this file's summary, not the plan's text.
var acceptanceTitles = []struct{ id, title string }{
	{"SP17-M7-01", "installed launcher/manifest/tool discovery; bundle identifiers and checksums"},
	{"SP17-M7-02", "awkward paths, managed restrictions, unsupported optimizations disabled"},
	{"SP17-M7-03", "denied/symlink/secret/archive/decompression bounds"},
	{"SP17-M7-04", "forced failure at each publication boundary; no newly dangling pointer"},
	{"SP17-M7-05", "old readers, import restart, rollback before and after new writes"},
	{"SP17-M7-06", "independent kill switches, unknown schemas, uninstall retained-data choice"},
	{"SP17-M7-07", "release accounting: outcomes, uncertainty, complete-or-unknown"},
	{"SP17-M7-08", "platforms, licences, version/name checks, rollback rehearsal attached"},
}

// buildScopeReport applies every rule to the collected evidence.
func buildScopeReport(ev scopeEvidence) scopeReport {
	rep := scopeReport{Evidence: ev.Root}
	for _, t := range releaseTargets {
		rep.Targets = append(rep.Targets, targetRow(ev, t.GOOS+"/"+t.GOARCH))
	}
	for _, row := range acceptanceTitles {
		rep.Acceptance = append(rep.Acceptance, acceptanceRow(ev, row.id))
	}
	rep.Acceptance = append(rep.Acceptance, scopeAcceptanceRow{
		ID:     "SP17-M7-07 / live-task layer",
		Status: scopeExcluded,
		Note: "no live model runs are executed by this repository's tests; eval.LiveRunner is nil in " +
			"every shipped build and is deliberately left unwired (ruling R7-2)",
	})
	return rep
}

// targetRow computes one target's strongest status. Each candidate may only raise the row, so the
// order the collectors run in cannot change the answer.
func targetRow(ev scopeEvidence, target string) scopeTargetRow {
	row := scopeTargetRow{Target: target, Status: scopeUnknown}
	raise := func(status, artifact string) {
		if scopeTargetRank[status] > scopeTargetRank[row.Status] {
			row.Status, row.Artifact = status, artifact
		}
	}
	for _, family := range []string{"platform", "security", "fault", "switches"} {
		for _, rec := range ev.ByFamily[family] {
			if rec.targetKey() == target {
				raise(scopeBuilt, rec.citation())
			}
		}
	}
	if hv := ev.HostValidation; hv != nil && hv.targetKey() == target {
		raise(scopeBuilt, hv.citation())
		if hv.Outcome == outcomeAccepted || hv.Outcome == "validated" {
			raise(scopeValidated, hv.citation())
		}
	}
	for _, rec := range ev.ByFamily["install"] {
		if rec.targetKey() == target && rec.Capability == "install" && rec.Outcome == scopeVerified {
			raise(scopeInstalled, rec.citation())
		}
	}
	return row
}

// acceptanceRow applies one row's rule.
func acceptanceRow(ev scopeEvidence, id string) scopeAcceptanceRow {
	switch id {
	case "SP17-M7-01":
		row, _ := installCapabilityRow(ev, "install")
		if row.Status != scopeVerified && row.Artifact == "" && ev.HostValidation != nil {
			row.Artifact = ev.HostValidation.citation()
			row.Note = "directory validation only: the bundle was validated where it sits, which is " +
				"not an installed-plugin canary (Qompack.md §7.5)"
		}
		return row
	case "SP17-M7-02":
		return familyRow(ev, "platform")
	case "SP17-M7-03":
		return familyRow(ev, "security")
	case "SP17-M7-04":
		return familyRow(ev, "fault")
	case "SP17-M7-05":
		row, _ := installCapabilityRow(ev, "rollback")
		return row
	case "SP17-M7-06":
		return switchesAndUninstallRow(ev)
	case "SP17-M7-07":
		return accountingRow(ev)
	case "SP17-M7-08":
		return attachmentsRow(ev)
	}
	return scopeAcceptanceRow{ID: id, Status: scopeUnverified}
}

// familyRow is verified when the family has records and none of them failed. A `skipped` record
// neither raises nor lowers: it is a case that did not run, and the matrix page says why.
func familyRow(ev scopeEvidence, family string) scopeAcceptanceRow {
	id := map[string]string{
		"platform": "SP17-M7-02", "security": "SP17-M7-03",
		"fault": "SP17-M7-04", "switches": "SP17-M7-06",
	}[family]
	recs := ev.ByFamily[family]
	if len(recs) == 0 {
		return scopeAcceptanceRow{ID: id, Status: scopeUnverified, Note: "no " + family + " records are attached"}
	}
	verified, skipped := 0, 0
	for _, rec := range recs {
		switch rec.Outcome {
		case "failed":
			return scopeAcceptanceRow{
				ID: id, Status: scopeUnverified, Artifact: rec.citation(),
				Note: "a " + family + " record reports failed: " + rec.Name,
			}
		case scopeVerified:
			verified++
		case "skipped":
			skipped++
		}
	}
	return scopeAcceptanceRow{
		ID: id, Status: scopeVerified, Artifact: family + " records",
		Note: fmt.Sprintf("%d record(s): %d verified, %d skipped, 0 failed", len(recs), verified, skipped),
	}
}

// installCapabilityRow is verified when at least one install record of the named capability is
// verified, and pinned to unverified by any failed one. The bool is true when a failed record
// spoke and pinned the row.
func installCapabilityRow(ev scopeEvidence, capability string) (scopeAcceptanceRow, bool) {
	id := map[string]string{"install": "SP17-M7-01", "rollback": "SP17-M7-05"}[capability]
	row := scopeAcceptanceRow{
		ID: id, Status: scopeUnverified,
		Note: "no " + capability + " record is attached",
	}
	for _, rec := range ev.ByFamily["install"] {
		if rec.Capability != capability {
			continue
		}
		if rec.Outcome == "failed" {
			return scopeAcceptanceRow{
				ID: id, Status: scopeUnverified, Artifact: rec.citation(),
				Note: "a " + capability + " record reports failed: " + rec.Name,
			}, true
		}
		if rec.Outcome == scopeVerified && row.Status != scopeVerified {
			row = scopeAcceptanceRow{ID: id, Status: scopeVerified, Artifact: rec.citation()}
		}
	}
	return row, false
}

// switchesAndUninstallRow needs BOTH halves of SP17-M7-06: the switch matrix this commit produces,
// and Task 8's uninstall and unknown-schema records. Either half alone leaves it unverified.
func switchesAndUninstallRow(ev scopeEvidence) scopeAcceptanceRow {
	row := scopeAcceptanceRow{ID: "SP17-M7-06", Status: scopeUnverified}
	switches := familyRow(ev, "switches")
	var missing []string
	if len(ev.ByFamily["switches"]) == 0 {
		missing = append(missing, "the switch matrix")
	} else if switches.Status != scopeVerified {
		missing = append(missing, "the switch matrix ("+switches.Note+")")
	}
	for _, capability := range []string{"uninstall", "unknown_schema"} {
		capRow, failed := installCapabilityRow(ev, capability)
		if failed {
			return scopeAcceptanceRow{
				ID: "SP17-M7-06", Status: scopeUnverified,
				Artifact: capRow.Artifact, Note: capRow.Note,
			}
		}
		if capRow.Status != scopeVerified {
			missing = append(missing, "a verified "+capability+" record")
		}
	}
	if len(missing) == 0 {
		row.Status = scopeVerified
		row.Artifact = "switches + install records"
		return row
	}
	row.Note = "still missing: " + strings.Join(missing, "; ")
	if len(ev.ByFamily["switches"]) > 0 {
		row.Artifact = "switches records"
	}
	return row
}

// accountingRow is SP17-M7-07: raised only by a committed release-check.json record. outcome is
// verified iff ok == true and no step has status FAIL; a FAIL pins the row unverified citing that
// record. Absent → unverified with the note below. Prose (commit7-evidence.md) never raises it.
func accountingRow(ev scopeEvidence) scopeAcceptanceRow {
	row := scopeAcceptanceRow{
		ID: "SP17-M7-07", Status: scopeUnverified,
		Note: "accounting attached; no release-check record verifies it",
	}
	if ev.ReleaseCheck == nil {
		return row
	}
	if ev.ReleaseCheck.outcome == "failed" {
		note := "release-check record reports failed"
		if ev.ReleaseCheck.failStep != "" {
			note = "a release-check step reports FAIL: " + ev.ReleaseCheck.failStep
		}
		return scopeAcceptanceRow{
			ID: "SP17-M7-07", Status: scopeUnverified,
			Artifact: ev.ReleaseCheck.file, Note: note,
		}
	}
	return scopeAcceptanceRow{
		ID: "SP17-M7-07", Status: scopeVerified, Artifact: ev.ReleaseCheck.file,
	}
}

// attachmentsRow is SP17-M7-08: the committed release-check record is verified (that record
// already covers licenses --check and version agreement as steps) AND at least one rollback
// install record is verified AND no failed rollback record is present.
func attachmentsRow(ev scopeEvidence) scopeAcceptanceRow {
	row := scopeAcceptanceRow{ID: "SP17-M7-08", Status: scopeUnverified}
	var missing []string
	if ev.ReleaseCheck == nil || ev.ReleaseCheck.outcome != scopeVerified {
		missing = append(missing, "a verified release-check record")
	}
	rollback, rollbackFailed := installCapabilityRow(ev, "rollback")
	if rollbackFailed {
		return scopeAcceptanceRow{
			ID: "SP17-M7-08", Status: scopeUnverified,
			Artifact: rollback.Artifact, Note: rollback.Note,
		}
	}
	if rollback.Status != scopeVerified {
		missing = append(missing, "a verified rollback rehearsal record")
	}
	if len(missing) == 0 {
		row.Status = scopeVerified
		row.Artifact = "release-check.json + install records"
		return row
	}
	row.Note = "still missing: " + strings.Join(missing, "; ")
	return row
}

// renderScopeMarkdown prints the two tables plus the legend that makes them readable without this
// source file beside them.
func renderScopeMarkdown(rep scopeReport) string {
	var b strings.Builder
	b.WriteString("## Supported scope\n\n")
	fmt.Fprintf(&b, "Derived by `go run ./tools/devtool release-scope` from the committed records under `%s`.\n", rep.Evidence)
	b.WriteString("A status is raised only by a record with an `outcome` (for `release-check.json`, " +
		"derived from `ok` and its step statuses); prose never raises one.\n\n")
	b.WriteString("| target | status | artifact |\n| --- | --- | --- |\n")
	for _, r := range rep.Targets {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", r.Target, r.Status, orDash(r.Artifact))
	}
	b.WriteString("\n`installed-verified` > `directory-validated` > `built` > `unknown`. ")
	b.WriteString("`built` means a committed record names that target; a cross-compile proven only by CI's\n")
	b.WriteString("`crossbuild` job leaves no record here and so reads `unknown`.\n\n")

	b.WriteString("## Acceptance rows\n\n")
	b.WriteString("| row | status | artifact | note |\n| --- | --- | --- | --- |\n")
	for _, r := range rep.Acceptance {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", r.ID, r.Status, orDash(r.Artifact), orDash(r.Note))
	}
	b.WriteString("\n`verified` = a record says so. `unverified` = no record says so, which is not a claim\n")
	b.WriteString("that it is broken. `excluded` = deliberately out of scope, with the reason stated.\n")
	return b.String()
}
