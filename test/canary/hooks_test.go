package canary

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/contract"
)

// hookSubcommands are the six §7.3 hook entry points, spelled as `qompack` dispatches them
// (internal/cli/hooks.go), paired with the host event each is registered for in
// plugin/hooks/hooks.json.
//
// session-start is deliberately absent from the invalid-payload canary below: it is the one hook
// with a body of its own that starts the daemon before sending, and a canary that spawned a daemon
// into a temporary tree would stop being hermetic. Its own mechanism is probed by
// TestCanary_SessionStartReinjection instead.
var hookSubcommands = []struct {
	args  []string
	event string
}{
	{[]string{"observe", "tool"}, "PostToolUse"},
	{[]string{"observe", "prompt"}, "UserPromptSubmit"},
	{[]string{"observe", "stop"}, "Stop"},
	{[]string{"observe", "stop", "--subagent"}, "SubagentStop"},
	{[]string{"checkpoint"}, "PreCompact"},
	{[]string{"flush"}, "SessionEnd"},
}

// oversizedPayloadBytes is comfortably beyond the read limit the hook skeleton imposes
// (MaxPayloadBytes × 4, i.e. 4 MiB with the shipped default), so hookio.ReadEvent must refuse it
// rather than buffer it.
const oversizedPayloadBytes = 5 << 20

// TestCanary_InvalidHookPayload is M0-03's "invalid hook payloads" canary.
//
// A host that hands a hook something malformed, empty or enormous must not get a broken turn back.
// §2.3 fixes the contract: a hook ALWAYS exits 0, and whatever reaches stdout must be something the
// host can parse. This exercises the real built binary against all three shapes, on every hook the
// manifest registers.
//
// Scope, stated plainly: this is the REPOSITORY's binary in a temporary project with the daemon
// disabled. It establishes that the hook adapter and its exit contract survive bad input. It does
// not establish how an INSTALLED plugin behaves when the host itself produces such a payload.
func TestCanary_InvalidHookPayload(t *testing.T) {
	bin := buildQompack(t)
	root, env := tempProject(t)
	tgt := hostTarget(t)

	payloads := []struct {
		name  string
		stdin []byte
	}{
		{"malformed-json", []byte(`{"hook_event_name": "PostToolUse", "session_id":`)},
		{"empty-stdin", nil},
		{"not-json-at-all", []byte("this is not a hook payload\n")},
		{"json-but-not-an-object", []byte(`["hook_event_name"]`)},
		{"oversized", []byte(`{"hook_event_name":"PostToolUse","session_id":"canary","tool_response":"` +
			strings.Repeat("q", oversizedPayloadBytes) + `"}`)},
	}

	var problems []string
	for _, h := range hookSubcommands {
		for _, p := range payloads {
			name := strings.Join(h.args, " ") + "/" + p.name
			stdout, stderr, code := runBin(t, bin, h.args, p.stdin, env)
			if code != 0 {
				problems = append(problems, fmt.Sprintf("%s exited %d", name, code))
			}
			if !emptyOrParseableJSON(stdout) {
				problems = append(problems, fmt.Sprintf("%s wrote %d bytes of unparseable stdout", name, len(stdout)))
			}
			// stderr is allowed — Dispatch reports a usage error there and still exits 0 — but an
			// enormous one means the hook is dumping the payload back out, which is a privacy
			// problem as much as a noise one.
			if len(stderr) > 4096 {
				problems = append(problems, fmt.Sprintf("%s wrote %d bytes of stderr", name, len(stderr)))
			}
		}
	}

	rec := Record{
		Name:       "invalid_hook_payload",
		Capability: contract.CapObservation,
		Scope:      ScopeRepository,
		Target:     tgt,
		Outcome:    OutcomeVerified,
		Reason: fmt.Sprintf("repository-level: %d hook entry points × %d malformed, empty, "+
			"non-object and oversized (%d bytes) payloads against this build's own binary in a "+
			"temporary project with the daemon disabled. Every invocation exited 0 with "+
			"empty-or-parseable stdout. Installed-host payload production is unverified.",
			len(hookSubcommands), len(payloads), oversizedPayloadBytes),
	}
	if len(problems) > 0 {
		rec.Outcome = OutcomeFailed
		rec.Reason = "the always-exit-0 hook contract was not honoured: " + strings.Join(problems, "; ")
	}
	writeRecord(t, rec)

	require.Empty(t, problems,
		"§2.3: a hook exits 0 and writes host-parseable stdout whatever it is handed.\n"+
			"project: %s", root)
}

// TestCanary_CompetingHooks is M0-03's "competing hooks" canary.
//
// Claude Code runs a matcher's hooks concurrently, and a user may have other plugins registered for
// the same events. Two Qompack hooks racing on one project must both exit 0 and neither may corrupt
// the other's spool.
//
// Its limit is stated in the record rather than hidden: this races TWO COPIES OF QOMPACK against
// each other. Whether a competing hook from a DIFFERENT installed plugin interferes cannot be
// established without installing one into a disposable target, which M0-03 forbids here.
func TestCanary_CompetingHooks(t *testing.T) {
	bin := buildQompack(t)
	root, env := tempProject(t)
	tgt := hostTarget(t)

	const sessions = 2
	type result struct {
		session string
		code    int
		clean   bool
	}
	results := make([]result, sessions)

	var wg sync.WaitGroup
	for i := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			session := fmt.Sprintf("canary-session-%d", i)
			stdout, _, code := runBin(t, bin, []string{"observe", "tool"},
				hookPayload(t, "PostToolUse", session, root), env)
			results[i] = result{session: session, code: code, clean: emptyOrParseableJSON(stdout)}
		}()
	}
	wg.Wait()

	var problems []string
	for _, r := range results {
		if r.code != 0 {
			problems = append(problems, fmt.Sprintf("%s exited %d", r.session, r.code))
		}
		if !r.clean {
			problems = append(problems, r.session+" wrote unparseable stdout")
		}
	}

	rec := Record{
		Name:       "competing_hooks",
		Capability: contract.CapObservation,
		Scope:      ScopeRepository,
		Target:     tgt,
		Outcome:    OutcomeVerified,
		Reason: fmt.Sprintf("local approximation; competing installed plugin hooks unverified. "+
			"%d concurrent `observe tool` invocations for %d distinct session ids against one "+
			"temporary project (daemon disabled, so both spool). Both exited 0 with "+
			"empty-or-parseable stdout. This races two copies of Qompack, not Qompack against "+
			"another plugin's hook, which would require installing one into a disposable target.",
			sessions, sessions),
	}
	if len(problems) > 0 {
		rec.Outcome = OutcomeFailed
		rec.Reason = "concurrent hook invocations did not both honour the contract: " + strings.Join(problems, "; ")
	}
	writeRecord(t, rec)

	require.Empty(t, problems, "project: %s", root)
}
