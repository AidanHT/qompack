package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// fsckOrderReads is how many fsck reads TestFsck_RepeatedReadsOfADamagedStoreAgree compares with
// the first, in each of --json and text. The seeded files list has fsckOrderNeverLogged members
// drawn from one Go map, large enough that the runtime's per-range randomization reorders it on
// nearly every read: on the unfixed code the first --json comparison already differed. The smaller
// lists (three pointer labels, six pins, six drain records, five corrupt roots) reorder less often,
// which is why one row carries them all and compares many reads rather than two.
const fsckOrderReads = 20

// fsckOrderNeverLogged is how many paths the seed adds to index/files.json that the log never
// mentions: more than fsckMaxDetail, so the capped sample itself must be the same members.
const fsckOrderNeverLogged = fsckMaxDetail + 5

// seedFsckOrderDamage damages a seeded project in every check whose detail lines come from a Go
// map: the files view (paths the log never mentions), the pins view (ids the log never carried),
// state/drain.json (several refused progress records), one root line whose three recovery
// pointers do not resolve, and several roots that restore as corrupt (the fidelity check walks the
// roots map).
func seedFsckOrderDamage(t *testing.T, p seededProject) {
	t.Helper()

	// Roots that restore as corrupt: each declares as its recovery record the fixture's own root,
	// whose payload is Go source, not a delta record (store.ReadDelta: unparseable is corrupt).
	rootsPath := filepath.Join(p.Layot.Index, "roots.jsonl")
	raw, err := os.ReadFile(paths.Long(rootsPath))
	require.NoError(t, err)
	var fixture map[string]any
	first := strings.SplitN(strings.TrimSpace(string(raw)), "\n", 2)[0]
	require.NoError(t, json.Unmarshal([]byte(first), &fixture))
	require.Equal(t, fixtureRootHash(t, p).String(), fixture["root"], "the first root line is the fixture's")
	for i := range 4 {
		line := map[string]any{
			"v": 2, "op": "", "root": "sha256:" + strings.Repeat(fmt.Sprintf("a%d", i), 32), "ts": 3,
			"tool": "Read", "path": fmt.Sprintf("src/corrupt%d.go", i),
			"chunks": fixture["chunks"], "deltas": fixture["root"],
		}
		b, marshalErr := json.Marshal(line)
		require.NoError(t, marshalErr)
		appendLine(t, rootsPath, string(b))
	}

	viewPath := filepath.Join(p.Layot.Index, "files.json")
	raw, err = os.ReadFile(paths.Long(viewPath))
	require.NoError(t, err)
	var view map[string]any
	require.NoError(t, json.Unmarshal(raw, &view))
	files, _ := view["files"].(map[string]any)
	require.NotNil(t, files, "the seeded view carries a files map")
	versions := files["src/main.go"]
	require.NotNil(t, versions, "the seeded view records src/main.go")
	for i := range fsckOrderNeverLogged {
		files[fmt.Sprintf("src/never-logged-%02d.go", i)] = versions
	}
	raw, err = json.Marshal(view)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(viewPath), raw, 0o600))

	pinsPath := filepath.Join(p.Layot.Pins, "invariants.json")
	raw, err = os.ReadFile(paths.Long(pinsPath))
	require.NoError(t, err)
	var pinsView []map[string]any
	require.NoError(t, json.Unmarshal(raw, &pinsView))
	for i := range 6 {
		pinsView = append(pinsView, map[string]any{"id": fmt.Sprintf("inv-retired-%02d", i)})
	}
	raw, err = json.Marshal(pinsView)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(pinsPath), raw, 0o600))

	drain := map[string]any{}
	for i := range 6 {
		drain[fmt.Sprintf("client-%d.ndjson", 100+i)] = map[string]any{"size": 1, "offset": 5}
	}
	raw, err = json.Marshal(drain)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(paths.Long(filepath.Join(p.Layot.State, "drain.json")), raw, 0o600))

	appendLine(t, filepath.Join(p.Layot.Index, "roots.jsonl"), fmt.Sprintf(
		`{"v":2,"op":"","root":"sha256:%s","ts":2,"tool":"Read","path":"b.txt","chunks":[],`+
			`"deltas":"sha256:%s","base":"sha256:%s","orig":"sha256:%s"}`,
		strings.Repeat("ef", 32), strings.Repeat("c1", 32), strings.Repeat("c2", 32),
		strings.Repeat("c3", 32)))
}

// TestFsck_RepeatedReadsOfADamagedStoreAgree is D53(a) for fsck: two reads of an unchanged store
// say the same thing in the same order. Five checks appended their detail lines while ranging over
// a Go map, so every read of a damaged store listed them in a different order, and past
// fsckMaxDetail the capped sample named different members (w20 status audit). Both the --json
// document and the text page must be byte-identical across reads, and the capped files sample must
// be the first fsckMaxDetail paths in sorted order.
func TestFsck_RepeatedReadsOfADamagedStoreAgree(t *testing.T) {
	t.Parallel()

	p := seedFsckProject(t)
	seedFsckOrderDamage(t, p)

	read := func(args ...string) string {
		t.Helper()
		code, out, errw := fsckDispatch(t, append([]string{"fsck", "--project", p.Root}, args...)...)
		require.Equal(t, ExitError, code, "the seeded damage is a defect; stderr=%s", errw)
		return out
	}
	firstJSON, firstText := read("--json"), read()
	for i := range fsckOrderReads - 1 {
		require.Equal(t, firstJSON, read("--json"), "fsck --json read %d differs from the first", i+2)
		require.Equal(t, firstText, read(), "fsck text read %d differs from the first", i+2)
	}

	var doc map[string]any
	require.NoError(t, json.Unmarshal([]byte(firstJSON), &doc))
	files := fsckRequireRow(t, doc, "index.files")
	detail := fsckDetail(files)
	for i := range fsckMaxDetail - 1 {
		a := strings.Index(detail, fmt.Sprintf("never-logged-%02d.go", i))
		b := strings.Index(detail, fmt.Sprintf("never-logged-%02d.go", i+1))
		require.True(t, a >= 0 && b > a, "the capped sample lists paths %02d and %02d in order:\n%s",
			i, i+1, detail)
	}
	require.NotContains(t, detail, fmt.Sprintf("never-logged-%02d.go", fsckOrderNeverLogged-1),
		"the cap keeps the first paths in sorted order, not whichever the map yields")
	for _, id := range []string{"pins", "spool", "index.roots", "fidelity"} {
		require.Equal(t, false, fsckRequireRow(t, doc, id)["ok"], "row %s carries seeded damage", id)
	}
}
