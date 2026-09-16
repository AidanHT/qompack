package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scopeFixture writes an evidence directory from a map of relative path -> file body and returns
// it. Records are written as literal JSON so the fixtures are in exactly the shape the committed
// evidence and install-record-schema.md describe, rather than in whatever this package's structs
// happen to marshal to.
func scopeFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// scopeOf collects and builds a report over a fixture directory.
func scopeOf(t *testing.T, dir string) scopeReport {
	t.Helper()
	ev, err := collectScopeEvidence(dir, "fixture")
	if err != nil {
		t.Fatalf("collectScopeEvidence: %v", err)
	}
	return buildScopeReport(ev)
}

// targetStatus returns one target's row from a report.
func targetStatus(t *testing.T, rep scopeReport, target string) scopeTargetRow {
	t.Helper()
	for _, r := range rep.Targets {
		if r.Target == target {
			return r
		}
	}
	t.Fatalf("no row for %s", target)
	return scopeTargetRow{}
}

// rowStatus returns one acceptance row from a report.
func rowStatus(t *testing.T, rep scopeReport, id string) scopeAcceptanceRow {
	t.Helper()
	for _, r := range rep.Acceptance {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no acceptance row %s", id)
	return scopeAcceptanceRow{}
}

// TestReleaseScope_EmptyEvidenceIsUnknown is the default nobody may skip past: with nothing
// attached, every target is unknown and every acceptance row unverified. A tool whose empty state
// looked like partial success would be worse than no tool.
func TestReleaseScope_EmptyEvidenceIsUnknown(t *testing.T) {
	rep := scopeOf(t, scopeFixture(t, nil))

	if len(rep.Targets) != len(releaseTargets) {
		t.Fatalf("%d target rows for %d release targets", len(rep.Targets), len(releaseTargets))
	}
	for _, r := range rep.Targets {
		if r.Status != scopeUnknown {
			t.Errorf("%s = %s with no evidence at all", r.Target, r.Status)
		}
	}
	for _, r := range rep.Acceptance {
		if r.ID == "SP17-M7-07 / live-task layer" {
			if r.Status != scopeExcluded {
				t.Errorf("the live-task layer must be excluded, not %s (ruling R7-2)", r.Status)
			}
			continue
		}
		if r.Status != scopeUnverified {
			t.Errorf("%s = %s with no evidence at all", r.ID, r.Status)
		}
	}
}

// TestReleaseScope_MissingDirectoryIsNotAnError covers a fresh checkout: the evidence directory
// does not exist yet, and the command must still answer rather than refuse.
func TestReleaseScope_MissingDirectoryIsNotAnError(t *testing.T) {
	rep := scopeOf(t, filepath.Join(t.TempDir(), "nope"))
	if len(rep.Targets) != len(releaseTargets) {
		t.Fatalf("a missing evidence directory produced %d target rows", len(rep.Targets))
	}
}

const scopeHostValidationAccepted = `{
  "kind": "claude-plugin-validate",
  "outcome": "accepted",
  "host": {"os": "windows", "arch": "amd64"},
  "bundle": {"name": "qompack", "version": "0.1.0", "target": {"os": "windows", "arch": "amd64"}}
}`

const scopeInstallVerified = `{
  "name": "install_marketplace_user_scope",
  "capability": "install",
  "scope": "installed_cli",
  "outcome": "verified",
  "reason": "",
  "target": {"provider": "claude-code", "version": "2.1.263", "platform": "windows/amd64", "date": "2026-09-15"},
  "bundle": {"name": "qompack", "version": "0.1.0", "target": "windows/amd64", "binary_sha256": "abc"},
  "artifact": "commit8-install-windows-amd64/install_marketplace_user_scope.json"
}`

// TestReleaseScope_TargetStatusLadder walks the four statuses in order, adding one record at a
// time, and asserts each one raises the row and cites the record that raised it.
func TestReleaseScope_TargetStatusLadder(t *testing.T) {
	platform := `{"name":"path-root-plain","target":{"os":"windows","arch":"amd64"},"outcome":"verified","reason":"x"}`

	built := scopeOf(t, scopeFixture(t, map[string]string{
		"commit2-platform-windows-amd64/path-root-plain.json": platform,
	}))
	if got := targetStatus(t, built, "windows/amd64"); got.Status != scopeBuilt ||
		!strings.Contains(got.Artifact, "path-root-plain") {
		t.Errorf("a platform record alone must give `built` citing itself, got %+v", got)
	}

	validated := scopeOf(t, scopeFixture(t, map[string]string{
		"commit2-platform-windows-amd64/path-root-plain.json": platform,
		"commit1-host-validation.json":                        scopeHostValidationAccepted,
	}))
	if got := targetStatus(t, validated, "windows/amd64"); got.Status != scopeValidated {
		t.Errorf("an accepted host validation must give `directory-validated`, got %+v", got)
	}

	installed := scopeOf(t, scopeFixture(t, map[string]string{
		"commit2-platform-windows-amd64/path-root-plain.json":               platform,
		"commit1-host-validation.json":                                      scopeHostValidationAccepted,
		"commit8-install-windows-amd64/install_marketplace_user_scope.json": scopeInstallVerified,
	}))
	got := targetStatus(t, installed, "windows/amd64")
	if got.Status != scopeInstalled || got.Artifact != "commit8-install-windows-amd64/install_marketplace_user_scope.json" {
		t.Errorf("a verified install record must give `installed-verified` citing its own artifact, got %+v", got)
	}
	// And no other target moved.
	if other := targetStatus(t, installed, "linux/arm64"); other.Status != scopeUnknown {
		t.Errorf("linux/arm64 = %s from windows/amd64 records", other.Status)
	}
}

// TestReleaseScope_AFailedRecordPinsItsRow is the property that keeps the accounting honest: one
// failed record holds its row at unverified no matter how many verified ones sit beside it.
func TestReleaseScope_AFailedRecordPinsItsRow(t *testing.T) {
	rep := scopeOf(t, scopeFixture(t, map[string]string{
		"commit3-security-windows-amd64/ok.json": `{"name":"ok","target":{"os":"windows","arch":"amd64"},"outcome":"verified"}`,
		"commit3-security-windows-amd64/bad.json": `{"name":"bad","target":{"os":"windows","arch":"amd64"},` +
			`"outcome":"failed","reason":"a returned finding"}`,
	}))
	got := rowStatus(t, rep, "SP17-M7-03")
	if got.Status != scopeUnverified || !strings.Contains(got.Note, "bad") {
		t.Errorf("a failed security record must pin SP17-M7-03 and name itself, got %+v", got)
	}

	clean := scopeOf(t, scopeFixture(t, map[string]string{
		"commit3-security-windows-amd64/ok.json":      `{"name":"ok","target":{"os":"windows","arch":"amd64"},"outcome":"verified"}`,
		"commit3-security-windows-amd64/skipped.json": `{"name":"s","target":{"os":"windows","arch":"amd64"},"outcome":"skipped","reason":"platform: x"}`,
	}))
	if got := rowStatus(t, clean, "SP17-M7-03"); got.Status != scopeVerified {
		t.Errorf("verified records with one skip must verify the row, got %+v", got)
	}
}

// TestReleaseScope_SwitchRowNeedsBothHalves pins SP17-M7-06: the switch matrix alone does not
// verify it, because the row also promises uninstall follows the retained-data choice.
func TestReleaseScope_SwitchRowNeedsBothHalves(t *testing.T) {
	switchesOnly := scopeOf(t, scopeFixture(t, map[string]string{
		"commit7-switches-windows-amd64/mode_off.json": `{"name":"mode_off","capability":"switch",` +
			`"target":{"os":"windows","arch":"amd64"},"outcome":"verified"}`,
	}))
	got := rowStatus(t, switchesOnly, "SP17-M7-06")
	if got.Status != scopeUnverified || !strings.Contains(got.Note, "uninstall") {
		t.Errorf("the switch matrix alone must leave SP17-M7-06 unverified and say what is missing, got %+v", got)
	}

	both := scopeOf(t, scopeFixture(t, map[string]string{
		"commit7-switches-windows-amd64/mode_off.json": `{"name":"mode_off","capability":"switch",` +
			`"target":{"os":"windows","arch":"amd64"},"outcome":"verified"}`,
		"commit8-install-windows-amd64/uninstall.json": `{"name":"uninstall_keeps_data","capability":"uninstall",` +
			`"outcome":"verified","target":{"platform":"windows/amd64"},"artifact":"commit8-install-windows-amd64/uninstall.json"}`,
		"commit8-install-windows-amd64/unknown.json": `{"name":"unknown_schema","capability":"unknown_schema",` +
			`"outcome":"verified","target":{"platform":"windows/amd64"},"artifact":"commit8-install-windows-amd64/unknown.json"}`,
	}))
	if got := rowStatus(t, both, "SP17-M7-06"); got.Status != scopeVerified {
		t.Errorf("both halves present must verify SP17-M7-06, got %+v", got)
	}
}

// TestReleaseScope_ProseNeverRaisesAnything is the rule the whole tool rests on: a Markdown page
// full of confident claims moves nothing.
func TestReleaseScope_ProseNeverRaisesAnything(t *testing.T) {
	rep := scopeOf(t, scopeFixture(t, map[string]string{
		"commit6-evidence.md": "# Everything is verified on all six targets\n\noutcome: verified\n",
		"notes.txt":           "install verified windows/amd64 linux/amd64\n",
	}))
	for _, r := range rep.Targets {
		if r.Status != scopeUnknown {
			t.Errorf("%s = %s, raised by prose", r.Target, r.Status)
		}
	}
	if got := rowStatus(t, rep, "SP17-M7-01"); got.Status != scopeUnverified {
		t.Errorf("SP17-M7-01 = %s, raised by prose", got.Status)
	}

	onlyPage := scopeOf(t, scopeFixture(t, map[string]string{
		"commit7-evidence.md": "# SP17-M7-07 is verified\n\nThe gate passed.\n",
	}))
	if got := rowStatus(t, onlyPage, "SP17-M7-07"); got.Status != scopeUnverified {
		t.Errorf("SP17-M7-07 = %s with only commit7-evidence.md; prose must not raise it", got.Status)
	}
	if got := rowStatus(t, onlyPage, "SP17-M7-08"); got.Status != scopeUnverified {
		t.Errorf("SP17-M7-08 = %s with only commit7-evidence.md; prose must not raise it", got.Status)
	}
}

const scopeReleaseCheckOK = `{
  "version": "0.1.0",
  "tag": "v0.1.0",
  "ok": true,
  "steps": [{"name": "licenses", "status": "PASS", "seconds": 1, "detail": ""}]
}`

const scopeReleaseCheckFAIL = `{
  "version": "0.1.0",
  "ok": false,
  "steps": [{"name": "ci-local cover", "status": "FAIL", "seconds": 1, "detail": "floor"}]
}`

// TestReleaseScope_ReleaseCheckVerifiedRaisesM707 is the only raise path for SP17-M7-07: an
// ok:true record with no FAIL step.
func TestReleaseScope_ReleaseCheckVerifiedRaisesM707(t *testing.T) {
	rep := scopeOf(t, scopeFixture(t, map[string]string{
		"commit7-evidence.md": "# accounting\n",
		"release-check.json":  scopeReleaseCheckOK,
	}))
	got := rowStatus(t, rep, "SP17-M7-07")
	if got.Status != scopeVerified || got.Artifact != "release-check.json" {
		t.Errorf("an ok:true release-check record must verify SP17-M7-07 citing itself, got %+v", got)
	}
}

// TestReleaseScope_ReleaseCheckFailPinsM707: a FAIL step keeps SP17-M7-07 unverified and cites
// the record.
func TestReleaseScope_ReleaseCheckFailPinsM707(t *testing.T) {
	rep := scopeOf(t, scopeFixture(t, map[string]string{"release-check.json": scopeReleaseCheckFAIL}))
	got := rowStatus(t, rep, "SP17-M7-07")
	if got.Status != scopeUnverified || got.Artifact != "release-check.json" ||
		!strings.Contains(got.Note, "ci-local cover") {
		t.Errorf("a FAIL step must pin SP17-M7-07 unverified citing the record, got %+v", got)
	}
}

// TestReleaseScope_FailedInstallPinsM701 keeps the failed install record when host validation
// is also present.
func TestReleaseScope_FailedInstallPinsM701(t *testing.T) {
	rep := scopeOf(t, scopeFixture(t, map[string]string{
		"commit1-host-validation.json": scopeHostValidationAccepted,
		"commit8-install-windows-amd64/install.json": `{"name":"install_launcher","capability":"install",` +
			`"outcome":"failed","reason":"host refused","target":{"platform":"windows/amd64"},` +
			`"artifact":"commit8-install-windows-amd64/install.json"}`,
	}))
	got := rowStatus(t, rep, "SP17-M7-01")
	if got.Status != scopeUnverified ||
		got.Artifact != "commit8-install-windows-amd64/install.json" ||
		!strings.Contains(got.Note, "install_launcher") {
		t.Errorf("a failed install record must pin SP17-M7-01 citing itself, got %+v", got)
	}
}

// TestReleaseScope_FailedRollbackPinsM708 propagates the failed rollback record's artifact and note.
func TestReleaseScope_FailedRollbackPinsM708(t *testing.T) {
	rep := scopeOf(t, scopeFixture(t, map[string]string{
		"commit8-install-windows-amd64/rollback.json": `{"name":"rollback_rehearsal","capability":"rollback",` +
			`"outcome":"failed","reason":"host refused","target":{"platform":"windows/amd64"},` +
			`"artifact":"commit8-install-windows-amd64/rollback.json"}`,
	}))
	got := rowStatus(t, rep, "SP17-M7-08")
	if got.Status != scopeUnverified ||
		got.Artifact != "commit8-install-windows-amd64/rollback.json" ||
		!strings.Contains(got.Note, "rollback_rehearsal") {
		t.Errorf("a failed rollback record must pin SP17-M7-08 citing itself, got %+v", got)
	}
}

// TestReleaseScope_FailedUninstallPinsM706 propagates the failed record's artifact and note.
func TestReleaseScope_FailedUninstallPinsM706(t *testing.T) {
	rep := scopeOf(t, scopeFixture(t, map[string]string{
		"commit7-switches-windows-amd64/mode_off.json": `{"name":"mode_off","capability":"switch",` +
			`"target":{"os":"windows","arch":"amd64"},"outcome":"verified"}`,
		"commit8-install-windows-amd64/uninstall.json": `{"name":"uninstall_keeps_data","capability":"uninstall",` +
			`"outcome":"failed","reason":"host refused","target":{"platform":"windows/amd64"},` +
			`"artifact":"commit8-install-windows-amd64/uninstall.json"}`,
		"commit8-install-windows-amd64/unknown.json": `{"name":"unknown_schema","capability":"unknown_schema",` +
			`"outcome":"verified","target":{"platform":"windows/amd64"}}`,
	}))
	got := rowStatus(t, rep, "SP17-M7-06")
	if got.Status != scopeUnverified ||
		got.Artifact != "commit8-install-windows-amd64/uninstall.json" ||
		!strings.Contains(got.Note, "uninstall_keeps_data") {
		t.Errorf("a failed uninstall record must pin SP17-M7-06 citing itself, got %+v", got)
	}
}

// TestReleaseScope_ReportsOutcomeLessAndUndecodable prints one stderr line for an outcome-less
// record and one for an undecodable Task 1 host-validation record.
func TestReleaseScope_ReportsOutcomeLessAndUndecodable(t *testing.T) {
	dir := scopeFixture(t, map[string]string{
		"commit2-platform-windows-amd64/no-outcome.json": `{"name":"x","target":{"os":"windows","arch":"amd64"}}`,
		"commit1-host-validation.json":                   `{not-json`,
	})
	stderr := captureStderr(t, func() {
		if _, err := collectScopeEvidence(dir, "fixture"); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(stderr, "no-outcome.json") || !strings.Contains(stderr, "no outcome") {
		t.Errorf("outcome-less record was not reported on stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "commit1-host-validation.json") {
		t.Errorf("undecodable host-validation was not reported on stderr:\n%s", stderr)
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	b, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestRenderScopeMarkdown checks the rendered page carries every row and its legend, since the
// release workflow pipes this straight into the draft release's notes.
func TestRenderScopeMarkdown(t *testing.T) {
	rep := scopeOf(t, scopeFixture(t, map[string]string{"commit1-host-validation.json": scopeHostValidationAccepted}))
	out := renderScopeMarkdown(rep)
	for _, want := range []string{
		"## Supported scope", "## Acceptance rows", "windows/amd64", scopeValidated,
		"SP17-M7-01", "SP17-M7-08", scopeExcluded, "prose never raises one",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the rendered page omits %q\n%s", want, out)
		}
	}
}
