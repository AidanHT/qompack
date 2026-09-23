package security

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/paths"
)

// The three host-policy refusal sentences internal/mcp renders (authorize.go). They are matched by
// phrase rather than imported, because the packaged binary is what is under test here and the
// constants are unexported there.
const (
	hostDenyPhrase        = "the host's current permission rules deny reading"
	hostAskPhrase         = "the host's current permission rules require approval"
	hostUnavailablePhrase = "host policy unavailable"
)

// hostForm is one retrieval call that materializes the archived secret.
type hostForm struct {
	name string
	tool string
	args map[string]any
}

// TestV6_HostReadRulesGovernArchivedRetrieval is V6-HOST-1 (close-out C1.9) through the installed
// bundle: a file captured by the real hook while no rule existed is then placed under a Claude Code
// permissions rule, and every retrieval form of the packaged `qompack mcp` server must stop serving
// it — refusing explicitly for deny and ask, and reporting "host policy unavailable" when a
// settings file is unreadable — without echoing the path or the marker.
//
// The daemon reads the sibling HOME the project harness sets, so the only settings in play are the
// ones this test writes (plus whatever managed policy the machine really has, which is exactly what
// a user's session would read too).
func TestV6_HostReadRulesGovernArchivedRetrieval(t *testing.T) {
	b := assembledBundle(t)
	base := tempBase(t)
	p := newProjectAt(t, base, "proj")
	p.Env["CLAUDE_CONFIG_DIR"] = ""
	t.Cleanup(func() { shutdownIfReachable(t, p.Root) })

	const (
		id      = "toolu_v6_host_read_rule"
		marker  = "V6-HOST-1-PACKAGED-ARCHIVE-5b18e2d4"
		secret  = "config/prod.secret"
		sess    = core.SessionID("sess-v6-host-read-rule")
		content = marker + " rotation credentials live here\n"
	)
	writeProjectFile(t, p, secret, content)
	runHook(t, b.Bin, p, []string{"session-start"}, sessionStartPayload(t, p.Root, sess))
	require.True(t, waitDaemonUp(t, p.Root))
	runHook(t, b.Bin, p, []string{"observe", "tool"}, readToolPayload(t, p.Root, sess, id, secret, content))
	requireIndexed(t, p.Root, id)
	shutdownIfReachable(t, p.Root)

	s := openStoreAt(t, p.Root)
	rec, err := s.ToolUse(context.Background(), core.ToolUseID(id))
	require.NoError(t, err)
	root, err := s.GetRoot(context.Background(), rec.Root)
	require.NoError(t, err)
	require.NotEmpty(t, root.Chunks)
	require.NoError(t, s.Close())

	child := startMCP(t, b.Bin, p)
	t.Cleanup(func() { child.stop(t) })
	child.handshake(t)

	forms := []hostForm{
		{"expand_tool_use_id", mcp.ToolExpand, map[string]any{"tool_use_id": id, "full": true}},
		{"expand_root_hash", mcp.ToolExpand, map[string]any{"hash": root.Hash.String(), "full": true}},
		{"expand_chunk_hash", mcp.ToolExpand, map[string]any{"hash": root.Chunks[0].Hash.String(), "full": true}},
		{"re_read_latest", mcp.ToolReRead, map[string]any{"path": secret, "full": true}},
		{"re_read_at_hash", mcp.ToolReRead, map[string]any{"path": secret, "at": root.Hash.String(), "full": true}},
	}
	if runtime.GOOS == "windows" {
		// Windows opens the file under these spellings too, and re_read serves the real name's
		// history for them, so a rule on the real name must hold for each (C1.9 review finding 1).
		// On a POSIX filesystem each names a different, uncaptured file.
		forms = append(forms,
			hostForm{"re_read_trailing_dot_alias", mcp.ToolReRead, map[string]any{"path": secret + ".", "full": true}},
			hostForm{"re_read_trailing_space_alias", mcp.ToolReRead, map[string]any{"path": secret + " ", "full": true}},
		)
	}
	for _, f := range forms {
		res := child.call(t, f.tool, f.args)
		require.Contains(t, res.Text, marker, "control: %s must serve before any rule exists", f.name)
	}

	observed := map[string]string{}
	settings := filepath.Join(p.Root, ".claude", "settings.json")
	phases := []struct {
		name   string
		file   string
		body   string
		phrase string
		shape  string
	}{
		{"deny", settings, `{"permissions":{"deny":["Read(./config/prod.secret)"]}}`, hostDenyPhrase, "denied(found=false)"},
		{"ask", settings, `{"permissions":{"ask":["Read(config/**)"]}}`, hostAskPhrase, "denied(found=false)"},
		{
			"unreadable", filepath.Join(p.Home, ".claude", "settings.json"), `{"permissions":{"deny":["Read(./x"]}}`,
			hostUnavailablePhrase, "unavailable",
		},
	}
	var failures []string
	for _, ph := range phases {
		require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(ph.file)), 0o700))
		require.NoError(t, os.WriteFile(paths.Long(ph.file), []byte(ph.body), 0o600))
		for _, f := range forms {
			res := child.call(t, f.tool, f.args)
			body, _ := decodeBody(res)
			key := ph.name + "/" + f.name
			observed[key] = describeEnvelope(res)
			switch {
			case strings.Contains(res.Text, marker):
				failures = append(failures, key+": served the archived marker")
			case strings.Contains(res.Text, "prod.secret"):
				failures = append(failures, key+": echoed the path")
			case observed[key] != ph.shape:
				failures = append(failures, fmt.Sprintf("%s: envelope %s, want %s", key, observed[key], ph.shape))
			case !strings.Contains(body.Reason, ph.phrase):
				failures = append(failures, fmt.Sprintf("%s: reason %q lacks %q", key, body.Reason, ph.phrase))
			}
		}
		recall := child.call(t, mcp.ToolRecall, map[string]any{"query": "rotation credentials", "k": 10})
		// recall's `denied` is a count, which retrievalBody (a bool) cannot decode, so it is read here
		// with its own shape. Earlier calls' ephemeral copies of the secret's expansions are withheld
		// and counted too, which is why the count grows from phase to phase.
		var r struct {
			Count      int    `json:"count"`
			Denied     int    `json:"denied"`
			HostPolicy string `json:"host_policy"`
		}
		if err := json.Unmarshal([]byte(recall.Text), &r); err != nil {
			failures = append(failures, ph.name+"/recall: not a JSON body: "+err.Error())
		}
		observed[ph.name+"/recall"] = fmt.Sprintf("hits=%d,denied=%d,host_policy=%t", r.Count, r.Denied, r.HostPolicy != "")
		if strings.Contains(recall.Text, marker) || strings.Contains(recall.Text, "prod.secret") {
			failures = append(failures, ph.name+"/recall: exposed the withheld hit")
		}
		if r.Denied < 1 {
			failures = append(failures, ph.name+"/recall: the withheld hit was not counted")
		}
		if (ph.name == "unreadable") != strings.Contains(r.HostPolicy, hostUnavailablePhrase) {
			failures = append(failures, fmt.Sprintf("%s/recall: host_policy %q", ph.name, r.HostPolicy))
		}
		// Each phase stands alone: removing its file must restore retrieval on the very next call.
		require.NoError(t, os.Remove(paths.Long(ph.file)))
		res := child.call(t, mcp.ToolExpand, map[string]any{"tool_use_id": id, "full": true})
		if !strings.Contains(res.Text, marker) {
			failures = append(failures, ph.name+": removing the settings did not restore retrieval")
		}
	}
	child.finish(t)

	keys := make([]string, 0, len(observed))
	for k := range observed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var detail []string
	for _, k := range keys {
		detail = append(detail, k+"="+observed[k])
	}
	rec2 := newRecord(t, "v6_host_read_rules_govern_archived_retrieval")
	rec2.Capability = CapArchiveTrust
	rec2.Detail = strings.Join(detail, "; ")
	rec2.Outcome = OutcomeVerified
	rec2.Reason = "every retrieval form of the packaged MCP server refused a path under a current " +
		"Claude Code deny or ask rule, and withheld path-bearing content while a settings file was unreadable"
	if len(failures) > 0 {
		rec2.Outcome = OutcomeFailed
		rec2.Reason = "archived retrieval did not honour the host's current Read rules: " +
			strings.Join(failures, "; ") + "; owner internal/mcp + internal/hostperm"
	}
	writeRecord(t, rec2)
	require.Empty(t, failures)
}
