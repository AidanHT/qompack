package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
)

// selfTestJSONFor runs `qompack self-test --json` in a fresh project whose config.json is body.
func selfTestJSONFor(t *testing.T, body string) (int, selfTestReport) {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(contract.ResetProducers)
	writeAdmissionConfig(t, dir, body)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), []Cmd{{Name: "self-test", Run: runSelfTest}},
		[]string{"qompack", "self-test", "--json"}, Env{
			Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": dir}),
			Stdin:   bytes.NewReader(nil),
			Clock:   testClock(),
			HomeDir: t.TempDir(),
		}, &out, &errw)
	var report selfTestReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report), "stdout=%s stderr=%s", out.String(), errw.String())
	return code, report
}

func selfTestFindCheck(t *testing.T, report selfTestReport, id string) selfTestCheck {
	t.Helper()
	for _, c := range report.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("self-test reported no %q check: %+v", id, report.Checks)
	return selfTestCheck{}
}

// TestSelfTest_ReportsACaptureConfigRefusal: when the hook path would refuse every delivery,
// self-test must say so as a critical failure. Before C1.8 it reported `config.load ok` and exited
// 0, because its only configuration check exercises the soft loader, which never refuses.
func TestSelfTest_ReportsACaptureConfigRefusal(t *testing.T) {
	code, report := selfTestJSONFor(t, `{"runtime":{"redact":{"patterns":"PRIVATE-[A-Z]{12}"}}}`)

	check := selfTestFindCheck(t, report, "config.capture")
	require.False(t, check.OK, "a configuration the hook path refuses is never ok: %+v", check)
	require.Equal(t, contract.SevCritical, check.Severity, "every hook recording nothing is critical")
	require.Contains(t, check.Observed, "refused")
	require.Contains(t, check.Detail, "runtime.redact", "the refusal names its structural class")
	require.NotContains(t, check.Detail, "PRIVATE-", "the refusal never echoes a configured value")
	require.Equal(t, ExitError, code)
	require.Equal(t, ExitError, report.Exit)
}

// TestSelfTest_ReportsACaptureSwitchRefusal: a runtime.mode the hook path cannot apply as written is
// a refusal of every delivery (config.TestLoadForCapture_RefusesAnyCaptureSwitchProblem), so
// self-test reports it as one — while config.load, whose soft loader still falls back, reads ok.
func TestSelfTest_ReportsACaptureSwitchRefusal(t *testing.T) {
	code, report := selfTestJSONFor(t, `{"runtime":{"mode":"PRIVATE-OFF"}}`)

	check := selfTestFindCheck(t, report, "config.capture")
	require.False(t, check.OK, "a configuration the hook path refuses is never ok: %+v", check)
	require.Equal(t, contract.SevCritical, check.Severity, "every hook recording nothing is critical")
	require.Contains(t, check.Observed, "refused")
	require.Contains(t, check.Detail, "runtime.mode", "the refusal names its structural class")
	require.NotContains(t, check.Detail, "PRIVATE-", "the refusal never echoes a configured value")
	require.True(t, selfTestFindCheck(t, report, "config.load").OK,
		"the soft loader falls back for the same file, which is why config.load alone is not the answer")
	require.Equal(t, ExitError, code)
}

// TestSelfTest_ReportsACaptureConfigDegradation: a configuration the hook path loads with a fallback
// or an ignored key is not "ok" either — it is a warning naming the keys, and capture continues.
func TestSelfTest_ReportsACaptureConfigDegradation(t *testing.T) {
	code, report := selfTestJSONFor(t, `{"retrieval":{"defaultSpan":"sideways"},"runtime":{"notAKey":1}}`)

	check := selfTestFindCheck(t, report, "config.capture")
	require.False(t, check.OK, "a degraded capture configuration is never reported ok: %+v", check)
	require.Equal(t, contract.SevWarn, check.Severity, "capture continues, so this is not critical")
	require.Contains(t, check.Detail, "retrieval.defaultSpan")
	require.Contains(t, check.Detail, "runtime.notAKey")
	require.Equal(t, ExitOK, code)
}

// TestSelfTest_CleanCaptureConfigIsOK keeps the new check honest in the other direction.
func TestSelfTest_CleanCaptureConfigIsOK(t *testing.T) {
	code, report := selfTestJSONFor(t, `{}`)

	check := selfTestFindCheck(t, report, "config.capture")
	require.True(t, check.OK, "%+v", check)
	require.Equal(t, contract.SevInfo, check.Severity)
	require.Equal(t, ExitOK, code)
}

// TestDoctor_ReportsTheCaptureConfiguration: doctor's recording-gaps section must name a capture
// configuration the hook path refuses, and one it applies only partly, including in a project whose
// .qompack holds nothing but the config file — which is exactly the state a refusal leaves behind.
func TestDoctor_ReportsTheCaptureConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		body     string
		status   string
		observed string
		detail   string
	}{
		{name: "clean", body: `{}`, status: doctorOK, observed: "applied as written"},
		{
			name: "refused", body: `{"runtime":{"redact":{"patterns":"PRIVATE-[A-Z]{12}"}}}`,
			status: doctorDegraded, observed: "refused", detail: "runtime.redact",
		},
		{
			name: "refused switch", body: `{"runtime":{"mode":false}}`,
			status: doctorDegraded, observed: "refused", detail: "runtime.mode",
		},
		{
			name: "degraded", body: `{"retrieval":{"defaultSpan":"sideways"},"runtime":{"notAKey":1}}`,
			status: doctorDegraded, observed: "fell back", detail: "runtime.notAKey",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeAdmissionConfig(t, root, tc.body)

			code, doc, errw := doctorJSON(t, root)
			require.Equal(t, ExitOK, code, "stderr=%s", errw)
			row := doctorFindRow(t, doc, "recording", "config.capture")
			require.Equal(t, tc.status, row["status"], "row=%v", row)
			require.Contains(t, row["observed"], tc.observed, "row=%v", row)
			if tc.detail != "" {
				require.Contains(t, row["detail"], tc.detail, "row=%v", row)
			}
			require.NotContains(t, doctorText(t, doc), "PRIVATE-", "doctor never echoes a refused value")
		})
	}
}

// TestDoctor_CaptureConfigResolvesARelativeProject: `doctor --project .` is an ordinary invocation,
// and the hooks never see doctor's flag — they resolve an absolute root of their own. The row must
// judge the configuration the hooks would load, not refuse the relative spelling of the flag.
func TestDoctor_CaptureConfigResolvesARelativeProject(t *testing.T) {
	root := t.TempDir()
	writeAdmissionConfig(t, root, `{}`)
	t.Chdir(root)

	code, doc, errw := doctorJSON(t, ".")
	require.Equal(t, ExitOK, code, "stderr=%s", errw)
	row := doctorFindRow(t, doc, "recording", "config.capture")
	require.Equal(t, doctorOK, row["status"], "row=%v", row)
}
