package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// V6-HOST-1 (close-out C1.9): every retrieval form refuses archived content whose original path the
// host's current Read rules deny or ask about, with a reason that is neither "not found" nor the
// containment refusal, and fails closed when those rules cannot be read. Pathless records keep
// their V6-AUTH behaviour.

const (
	hpSecret     = "config/secret.env"
	hpSecretBody = "archived marker V6-HOST-1-ARCHIVE-7c4e19b2 about the rotation window\n"
	hpSecretMark = "V6-HOST-1-ARCHIVE-7c4e19b2"
	hpOK         = "docs/ok.md"
	hpOKBody     = "# notes\nthe rotation window is documented here\n"
	hpShellBody  = "shell output mentions the rotation window too\n"
)

// hpFixture is a fixture holding one secret capture, one ordinary capture and one pathless shell
// capture, each as a tool use AND a file version where it has a path.
//
// Retrieval results are not re-stored as ephemeral records here, so every count a test asserts is
// a count of the three captures alone. Ephemeral copies are authorized like any other record — an
// `expand` of a path is re-stored under that path, and a recall result has no path provenance at
// all — which TestHostPolicy_EphemeralCopiesAreJudgedToo pins separately.
type hpFixture struct {
	*fixture
	secretRoot, okRoot, shellRoot core.Hash
	secretID, okID, shellID       core.ToolUseID
}

func newHPFixture(t *testing.T, opts ...fixtureOpt) *hpFixture {
	t.Helper()
	if len(opts) == 0 {
		opts = []fixtureOpt{withConfig(func(c *config.Config) { c.Retrieval.EphemeralResults = false })}
	}
	f := &hpFixture{fixture: newFixture(t, opts...)}
	f.secretRoot, f.secretID = f.putAndRecord(t, "Read", hpSecret, hpSecretBody, 1)
	f.Clock.Advance(time.Minute)
	f.okRoot, f.okID = f.putAndRecord(t, "Read", hpOK, hpOKBody, 2)
	f.shellRoot, f.shellID = f.putAndRecord(t, "Bash", "", hpShellBody, 3)
	return f
}

// settingsFile names one settings source of the fixture's hermetic host.
type settingsFile int

const (
	projectSettings settingsFile = iota
	localSettings
	userSettings
	managedSettings
)

// writeSettings writes a settings document into one source and stamps it outside the policy's
// racy window, so the test measures change detection by content and time together.
func (f *hpFixture) writeSettings(t *testing.T, which settingsFile, body string) {
	t.Helper()
	var p string
	switch which {
	case projectSettings:
		p = filepath.Join(f.Root, ".claude", "settings.json")
	case localSettings:
		p = filepath.Join(f.Root, ".claude", "settings.local.json")
	case userSettings:
		p = filepath.Join(f.Home, ".claude", "settings.json")
	case managedSettings:
		p = filepath.Join(f.Managed, "managed-settings.json")
	}
	require.NoError(t, os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700))
	require.NoError(t, os.WriteFile(paths.Long(p), []byte(body), 0o600))
	hpStamp = hpStamp.Add(time.Second)
	require.NoError(t, os.Chtimes(paths.Long(p), hpStamp, hpStamp))
}

// hpStamp advances with every settings write so each edit has a distinct, old timestamp.
var hpStamp = time.Now().Add(-24 * time.Hour)

// denyRules is a project settings document with the given deny entries.
func denyRules(rules ...string) string { return permsDoc("deny", rules) }

// askRules is a project settings document with the given ask entries.
func askRules(rules ...string) string { return permsDoc("ask", rules) }

func permsDoc(key string, rules []string) string {
	b, _ := json.Marshal(map[string]any{"permissions": map[string]any{key: rules}})
	return string(b)
}

// contentForms are every address form that materializes the secret's bytes.
func (f *hpFixture) contentForms(t *testing.T) map[string]struct {
	tool string
	args map[string]any
} {
	t.Helper()
	rt, err := f.Store.GetRoot(context.Background(), f.secretRoot)
	require.NoError(t, err)
	require.NotEmpty(t, rt.Chunks)
	at := core.NowMilli(f.Clock).Time().Add(time.Hour).Format(time.RFC3339)
	return map[string]struct {
		tool string
		args map[string]any
	}{
		"expand/tool_use_id":         {ToolExpand, map[string]any{"tool_use_id": string(f.secretID), "full": true}},
		"expand/root_hash":           {ToolExpand, map[string]any{"hash": f.secretRoot.String(), "full": true}},
		"expand/chunk_hash":          {ToolExpand, map[string]any{"hash": rt.Chunks[0].Hash.String(), "full": true}},
		"re_read/latest":             {ToolReRead, map[string]any{"path": hpSecret, "full": true}},
		"re_read/line_anchor":        {ToolReRead, map[string]any{"path": hpSecret + ":1"}},
		"re_read/at_turn":            {ToolReRead, map[string]any{"path": hpSecret, "at": "turn:1", "full": true}},
		"re_read/at_timestamp":       {ToolReRead, map[string]any{"path": hpSecret, "at": at, "full": true}},
		"re_read/at_hash":            {ToolReRead, map[string]any{"path": hpSecret, "at": f.secretRoot.String()}},
		"re_read/other_path_at_hash": {ToolReRead, map[string]any{"path": hpOK, "at": f.secretRoot.String()}},
	}
}

// requireRefused asserts one response is the explicit refusal want, exposes no secret byte and does
// not echo the path.
func requireRefused(t *testing.T, text string, want any) {
	t.Helper()
	require.NotContains(t, text, hpSecretMark, "archived content was served: %s", text)
	require.NotContains(t, text, `"found":true`, "a refusal must not claim the content was found")
	switch w := want.(type) {
	case deniedBody:
		var d deniedBody
		require.NoError(t, json.Unmarshal([]byte(text), &d), text)
		require.Equal(t, w, d, text)
	case missBody:
		var m missBody
		require.NoError(t, json.Unmarshal([]byte(text), &m), text)
		require.Equal(t, w, m, text)
	}
	require.NotContains(t, text, "secret.env", "a refusal must not echo the path")
}

func TestHostPolicy_EveryContentFormRefusesADeniedPath(t *testing.T) {
	f := newHPFixture(t)
	forms := f.contentForms(t)
	for name, form := range forms {
		body := responseText(f.call(t, form.tool, form.args))
		require.Contains(t, body, hpSecretMark, "control: %s must serve before any rule exists", name)
	}

	f.writeSettings(t, projectSettings, denyRules("Read(./config/secret.env)"))
	for name, form := range forms {
		t.Run(name, func(t *testing.T) {
			requireRefused(t, responseText(f.call(t, form.tool, form.args)), denied(hostDeniedReason))
		})
	}
	t.Run("the ordinary path still serves", func(t *testing.T) {
		body := spanContentOf(t, f.fixture, ToolExpand, map[string]any{"tool_use_id": string(f.okID)})
		require.True(t, body.Found)
		require.Equal(t, hpOKBody, body.Content)
	})
}

func TestHostPolicy_RecallWithholdsDeniedHits(t *testing.T) {
	f := newHPFixture(t)
	var before recallBody
	f.callOK(t, ToolRecall, map[string]any{"query": "rotation window", "k": 10}, &before)
	require.Equal(t, 3, before.Count, "control: all three captures match before any rule exists")

	f.writeSettings(t, projectSettings, denyRules("Read(*.env)"))
	resp := f.callOK(t, ToolRecall, map[string]any{"query": "rotation window", "k": 10}, nil)
	text := responseText(resp)
	require.NotContains(t, text, hpSecretMark)
	require.NotContains(t, text, "secret.env", "a withheld hit's pointer is withheld too")
	var after recallBody
	require.NoError(t, json.Unmarshal([]byte(text), &after))
	require.Equal(t, 2, after.Count)
	require.Equal(t, 1, after.Denied, "the withheld hit is counted, never silently dropped")
	require.Empty(t, after.HostPolicy, "a rule decided this, not an unreadable policy")
	var paths []string
	for _, h := range after.Hits {
		paths = append(paths, h.Path)
	}
	require.ElementsMatch(t, []string{hpOK, ""}, paths)
}

func TestHostPolicy_AskIsRefusedWithItsOwnReason(t *testing.T) {
	f := newHPFixture(t)
	f.writeSettings(t, projectSettings, askRules("Read(config/**)"))
	for name, form := range f.contentForms(t) {
		t.Run(name, func(t *testing.T) {
			requireRefused(t, responseText(f.call(t, form.tool, form.args)), denied(hostAskReason))
		})
	}
	var r recallBody
	f.callOK(t, ToolRecall, map[string]any{"query": "rotation window", "k": 10}, &r)
	require.Equal(t, 1, r.Denied)

	f.writeSettings(t, projectSettings, `{"permissions":{"ask":["Read(config/**)"],"deny":["Read(./config/secret.env)"]}}`)
	requireRefused(t, responseText(f.call(t, ToolExpand, map[string]any{"tool_use_id": string(f.secretID)})),
		denied(hostDeniedReason))
}

func TestHostPolicy_ReasonsAreDistinct(t *testing.T) {
	reasons := []string{hostDeniedReason, hostAskReason, hostUnavailableReason, authorizedDenialReason}
	seen := map[string]bool{}
	for _, r := range reasons {
		require.False(t, seen[r])
		seen[r] = true
		require.NotContains(t, strings.ToLower(r), "not found")
	}
}

func TestHostPolicy_UnreadableSettingsFailClosedForPathBearingContentOnly(t *testing.T) {
	f := newHPFixture(t)
	f.writeSettings(t, userSettings, `{"permissions":{"deny":["Read(./x"]}}`)
	for name, form := range f.contentForms(t) {
		t.Run(name, func(t *testing.T) {
			requireRefused(t, responseText(f.call(t, form.tool, form.args)), unavailable(hostUnavailableReason))
		})
	}
	t.Run("ordinary paths are withheld too", func(t *testing.T) {
		requireRefused(t, responseText(f.call(t, ToolExpand, map[string]any{"tool_use_id": string(f.okID)})),
			unavailable(hostUnavailableReason))
	})
	t.Run("pathless records keep their V6 behaviour", func(t *testing.T) {
		for _, args := range []map[string]any{
			{"tool_use_id": string(f.shellID)},
			{"hash": f.shellRoot.String()},
		} {
			body := spanContentOf(t, f.fixture, ToolExpand, args)
			require.True(t, body.Found)
			require.Equal(t, hpShellBody, body.Content)
		}
	})
	t.Run("recall says why its path-bearing hits are gone", func(t *testing.T) {
		var r recallBody
		f.callOK(t, ToolRecall, map[string]any{"query": "rotation window", "k": 10}, &r)
		require.Equal(t, 1, r.Count, "only the pathless hit survives")
		require.Equal(t, 2, r.Denied)
		require.Equal(t, hostUnavailableReason, r.HostPolicy)
	})
	t.Run("the counter and the loud log record the degradation", func(t *testing.T) {
		require.Positive(t, f.Metrics.Counter("mcp.host_policy_unavailable").Value())
	})
	t.Run("fixing the file restores retrieval on the next call", func(t *testing.T) {
		f.writeSettings(t, userSettings, `{"permissions":{}}`)
		body := spanContentOf(t, f.fixture, ToolExpand, map[string]any{"tool_use_id": string(f.secretID)})
		require.True(t, body.Found)
	})
}

func TestHostPolicy_PathlessRecordsKeepTheirV6Behaviour(t *testing.T) {
	f := newHPFixture(t)
	lostRoot, lostID := f.putAndRecord(t, "Read", "", "a Read whose path was lost\n", 4)
	f.writeSettings(t, projectSettings, denyRules("Read"))

	body := spanContentOf(t, f.fixture, ToolExpand, map[string]any{"tool_use_id": string(f.shellID)})
	require.Equal(t, hpShellBody, body.Content, "a deny-all Read rule has no path to match on shell output")
	for _, args := range []map[string]any{{"tool_use_id": string(lostID)}, {"hash": lostRoot.String()}} {
		var d deniedBody
		f.callOK(t, ToolExpand, args, &d)
		require.True(t, d.Denied)
		require.Equal(t, "authorization denied: the capture has no usable path provenance", d.Reason,
			"a lost path is still V6-AUTH's refusal, not the host's")
	}
	requireRefused(t, responseText(f.call(t, ToolExpand, map[string]any{"tool_use_id": string(f.okID)})),
		denied(hostDeniedReason))
}

func TestHostPolicy_RulesFromEverySourceApply(t *testing.T) {
	for name, which := range map[string]settingsFile{
		"project": projectSettings, "local": localSettings, "user": userSettings, "managed": managedSettings,
	} {
		t.Run(name, func(t *testing.T) {
			f := newHPFixture(t)
			f.writeSettings(t, which, denyRules("Read(//**/config/secret.env)"))
			requireRefused(t, responseText(f.call(t, ToolExpand, map[string]any{"tool_use_id": string(f.secretID)})),
				denied(hostDeniedReason))
		})
	}
}

func TestHostPolicy_AnEditIsSeenByTheNextCall(t *testing.T) {
	f := newHPFixture(t)
	args := map[string]any{"tool_use_id": string(f.secretID)}
	require.True(t, spanContentOf(t, f.fixture, ToolExpand, args).Found)
	f.writeSettings(t, projectSettings, denyRules("Read(./config/secret.env)"))
	requireRefused(t, responseText(f.call(t, ToolExpand, args)), denied(hostDeniedReason))
	require.NoError(t, os.Remove(paths.Long(filepath.Join(f.Root, ".claude", "settings.json"))))
	require.True(t, spanContentOf(t, f.fixture, ToolExpand, args).Found, "removing the rule restores retrieval")
}

func TestHostPolicy_CaseFollowsThePlatform(t *testing.T) {
	f := newHPFixture(t)
	f.writeSettings(t, projectSettings, denyRules("Read(./CONFIG/SECRET.ENV)"))
	text := responseText(f.call(t, ToolExpand, map[string]any{"tool_use_id": string(f.secretID)}))
	if paths.DefaultFold() {
		requireRefused(t, text, denied(hostDeniedReason))
		return
	}
	require.Contains(t, text, hpSecretMark, "on a case-sensitive platform the rule names a different file")
}

func TestHostPolicy_AnInProjectLinkIsJudgedWhereItLands(t *testing.T) {
	f := newFixture(t, withFiles(map[string]string{"real/key.pem": "k\n"}))
	link := filepath.Join(f.Root, "lnk")
	if err := makeDirLink(link, filepath.Join(f.Root, "real")); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction (" +
			runtime.GOOS + "): " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(paths.Long(link)) })
	_, id := f.putAndRecord(t, "Read", "lnk/key.pem", "LINKED-KEY-MATERIAL\n", 1)
	hp := &hpFixture{fixture: f}
	hp.writeSettings(t, projectSettings, denyRules("Read(./real/**)"))
	text := responseText(f.call(t, ToolExpand, map[string]any{"tool_use_id": string(id)}))
	require.NotContains(t, text, "LINKED-KEY-MATERIAL")
	var d deniedBody
	require.NoError(t, json.Unmarshal([]byte(text), &d))
	require.Equal(t, denied(hostDeniedReason), d, "a path through a link is denied when its target is")
}

func TestHostPolicy_WhyWithholdsDeniedEvidence(t *testing.T) {
	f := newHPFixture(t)
	cp := loadContractCheckpoint(t)
	cp.Decisions[0].Evidence = f.secretRoot
	cp.Decisions = append(cp.Decisions, checkpoint.Decision{
		ID: "dec_0000000000ab", What: "keep the docs", Why: "they explain the window", Evidence: f.okRoot,
	})
	f.Checks.Chained = []checkpoint.Checkpoint{cp}
	f.Checks.Refs = refsFor(cp.Seq)
	want := cp.Decisions[0]

	var ok whyBody
	f.callOK(t, ToolWhy, map[string]any{"decision_id": "dec_0000000000ab"}, &ok)
	require.NotNil(t, ok.EvidenceBytes, "control: authorized evidence is sized")
	require.NotEmpty(t, ok.Hint)
	require.Empty(t, ok.EvidenceWithheld)

	var before whyBody
	f.callOK(t, ToolWhy, map[string]any{"decision_id": string(want.ID)}, &before)
	require.NotNil(t, before.EvidenceBytes, "control: the secret's evidence is sized before the rule exists")

	f.writeSettings(t, projectSettings, denyRules("Read(./config/secret.env)"))
	var after whyBody
	f.callOK(t, ToolWhy, map[string]any{"decision_id": string(want.ID)}, &after)
	require.True(t, after.Found, "the decision itself is still answered")
	require.Equal(t, want.What, after.What)
	require.Nil(t, after.EvidenceBytes, "the evidence preview is withheld")
	require.Empty(t, after.Hint, "no hint may point at a refused expansion")
	require.Equal(t, hostDeniedReason, after.EvidenceWithheld)
}

func TestHostPolicy_DroppedWithholdsPointersIntoDeniedArchive(t *testing.T) {
	f := newHPFixture(t)
	entries := []checkpoint.DropEntry{
		{Kind: "tool_pointer", ID: string(f.secretID), Detail: "truncated at budget; expand(hash) still resolves"},
		{Kind: "file_pointer", ID: hpSecret, Detail: "truncated at budget; re_read(path) still resolves"},
		{Kind: "file_pointer", ID: hpOK, Detail: "truncated at budget; re_read(path) still resolves"},
		{Kind: "tool_pointer", ID: string(f.shellID), Detail: "truncated at budget"},
		{Kind: "decision", ID: "dec_0000000000cd", Detail: "truncated at budget; why(decision_id) still resolves"},
		{Kind: "file_pointer", ID: "never/captured.env", Detail: "truncated at budget"},
	}
	f.Drops.Entries = entries

	var before droppedBody
	f.callOK(t, ToolDropped, map[string]any{}, &before)
	require.Equal(t, entries, before.Drops, "control: nothing is withheld without a rule")

	f.writeSettings(t, projectSettings, denyRules("Read(*.env)"))
	resp := f.callOK(t, ToolDropped, map[string]any{}, nil)
	require.NotContains(t, responseText(resp), "secret.env")
	var after droppedBody
	require.NoError(t, json.Unmarshal([]byte(responseText(resp)), &after))
	require.Equal(t, entries[2:], after.Drops, "only pointers into denied archive are withheld")
	require.Equal(t, 2, after.Denied)
	require.Empty(t, after.HostPolicy)

	f.writeSettings(t, projectSettings, `{`)
	var broken droppedBody
	f.callOK(t, ToolDropped, map[string]any{}, &broken)
	require.Equal(t, 3, broken.Denied, "every pointer into path-bearing archive is withheld when the policy is unreadable")
	require.Equal(t, hostUnavailableReason, broken.HostPolicy)
}

// TestHostPolicy_TimelineCarriesNoPathBearingContent pins why `timeline` has no host check: its
// segments are turn ranges, timestamps, token counts and numeric features, never a path, a hash or
// archive text. A field added here that could carry one must add the check with it.
func TestHostPolicy_TimelineCarriesNoPathBearingContent(t *testing.T) {
	var keys []string
	typ := reflect.TypeOf(timelineSegment{})
	for i := 0; i < typ.NumField(); i++ {
		keys = append(keys, strings.Split(typ.Field(i).Tag.Get("json"), ",")[0])
	}
	sort.Strings(keys)
	require.Equal(t, []string{
		"checkpoint_seq", "closed", "encoded_once", "end_ts", "end_turn", "features", "id",
		"start_ts", "start_turn", "tokens",
	}, keys)

	f := newHPFixture(t)
	f.writeSettings(t, projectSettings, `{`)
	resp := f.call(t, ToolTimeline, map[string]any{})
	require.False(t, resp.IsError, "timeline answers even with an unreadable host policy: %s", responseText(resp))
}

func TestHostPolicy_DefaultPolicyIsBuiltFromTheProjectRoot(t *testing.T) {
	root := t.TempDir()
	require.NotNil(t, newHandlers(ToolDeps{ProjectRoot: root}).host,
		"a composition root that supplies no policy still gets the real environment's")

	h := newHandlers(ToolDeps{})
	require.Nil(t, h.host)
	require.Equal(t, unavailable(hostUnavailableReason), h.authorizeHost(context.Background(), "a.txt", "a.txt"),
		"no policy is never read as no rules")
}

func TestHostPolicy_OneSnapshotPerCall(t *testing.T) {
	f := newHPFixture(t)
	f.writeSettings(t, projectSettings, denyRules("Read(./nothing-here)"))
	var r recallBody
	f.callOK(t, ToolRecall, map[string]any{"query": "rotation window", "k": 10}, &r)
	require.Equal(t, 3, r.Count)
	h := newHandlers(f.Deps)
	ctx := h.withHostSnapshot(context.Background())
	a, err := h.hostRules(ctx)
	require.NoError(t, err)
	b, err := h.hostRules(ctx)
	require.NoError(t, err)
	require.Same(t, a, b, "every path a call checks is judged against one rule set")
}

func TestHostPolicy_EphemeralCopiesAreJudgedToo(t *testing.T) {
	f := newHPFixture(t, withConfig(func(*config.Config) {}))
	var expanded contentBody
	resp := f.callOK(t, ToolExpand, map[string]any{"tool_use_id": string(f.secretID), "full": true}, &expanded)
	require.Equal(t, hpSecretBody, expanded.Content, "control: served before any rule exists")
	copyID, ok := resp.Meta[metaToolUseID].(string)
	require.True(t, ok, "the expand result was re-stored as an ephemeral record")

	f.writeSettings(t, projectSettings, denyRules("Read(./config/secret.env)"))
	requireRefused(t, responseText(f.call(t, ToolExpand, map[string]any{"tool_use_id": copyID, "full": true})),
		denied(hostDeniedReason))
}

// storeWithoutProvenanceHP hides ContentOrigins, as authorize_v6_test.go's double does.
type storeWithoutProvenanceHP struct{ store.Store }

func TestHostPolicy_ReReadHashFormNeedsProvenance(t *testing.T) {
	f := newHPFixture(t)
	d := f.Deps
	d.Store = storeWithoutProvenanceHP{f.Store}
	h := newHandlers(d)
	resp, err := h.reRead(h.withHostSnapshot(context.Background()), Request{},
		json.RawMessage(`{"path":"`+hpOK+`","at":"`+f.okRoot.String()+`"}`))
	require.NoError(t, err)
	var m missBody
	require.NoError(t, json.Unmarshal([]byte(responseText(resp)), &m))
	no := false
	require.Equal(t, &no, m.Available, "a store that cannot name a hash's origins cannot authorize it")
}
