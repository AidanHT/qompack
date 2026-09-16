package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// corruptObjectPath finds the on-disk object file for h under root and returns it.
//
// The store's fanout is two levels of two hex digits — <objects>/<aa>/<bb>/<hex>[.zst] — and whether
// the leaf carries the .zst suffix depends on store.compression, so both spellings are tried.
func corruptObjectPath(t *testing.T, root string, h core.Hash) string {
	t.Helper()
	hex := strings.TrimPrefix(h.String(), "sha256:")
	dir := filepath.Join(paths.Of(root).Objects, hex[:2], hex[2:4])
	for _, name := range []string{hex + ".zst", hex} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(paths.Long(p)); err == nil {
			return p
		}
	}
	t.Fatalf("no object file on disk for %s under %s", h.Short(), dir)
	return ""
}

// damageObject flushes the store so h's bytes are on disk, then overwrites them with something that
// cannot decode to h.
//
// The flush is load-bearing: PutBytes buffers, so without it the object file this test means to
// damage does not exist yet and the corruption would be written into a path the store then
// overwrites with the real bytes.
func damageObject(t *testing.T, f *fixture, h core.Hash) {
	t.Helper()
	require.NoError(t, f.Store.Flush(context.Background()))
	p := corruptObjectPath(t, f.Root, h)
	require.NoError(t, os.WriteFile(paths.Long(p), []byte("not this object's bytes at all"), 0o600))
}

// makeDirLink installs a directory link at linkPath pointing at target, by whatever mechanism this
// host allows: a real symlink where privilege permits, and otherwise an NTFS junction, which needs
// none. Junctions are the shape a directory swap actually takes on Windows, and paths.ResolvesInside
// has to see through both.
func makeDirLink(linkPath, target string) error {
	if err := os.Symlink(target, linkPath); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	}
	// cmd's own `mklink` is the only mechanism the standard library does not expose, and it is a
	// builtin rather than an executable, hence `cmd /c`.
	out, err := exec.Command("cmd", "/c", "mklink", "/J", linkPath, target).CombinedOutput() //nolint:gosec // G204: fixed subcommand over this test's own temp directories
	if err != nil {
		return fmt.Errorf("mklink /J %s %s: %w: %s", linkPath, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// decodeMiss parses a response body as the miss/unavailable shape.
func decodeMiss(t *testing.T, text string) missBody {
	t.Helper()
	var b missBody
	require.NoError(t, json.Unmarshal([]byte(text), &b), "body was not JSON: %s", text)
	return b
}

// TestExpandReportsADamagedObjectAsUnavailable is finding S-3, on BOTH of expand's address forms.
//
// §12.3 makes a quarantined object a degradation to be continued through, and the retrieval layer
// already has the word for it: `unavailable`, an explicit third answer beside found and not-found.
// What the two forms answered instead were the two wrong answers. By tool_use_id the store refusal
// rendered through errResponse as a protocol-level TOOL ERROR; by bare chunk hash it was mapped to
// miss() — that is, to ABSENT, which tells a model the content was never there when in fact it was
// refused and preserved as evidence.
//
// There was no unit test for corrupt-object behaviour on either form before this one.
func TestExpandReportsADamagedObjectAsUnavailable(t *testing.T) {
	no := false

	t.Run("by tool_use_id", func(t *testing.T) {
		f, _, _, id := spanAuthObject(t)
		root := spanRootOf(t, f, mustToolUseRoot(t, f, id))
		damageObject(t, f, root.Chunks[0].Hash)

		resp := f.call(t, ToolExpand, map[string]any{"tool_use_id": string(id), "full": true})
		require.False(t, resp.IsError,
			"a refused object is a domain outcome, not a protocol error: %s", responseText(resp))

		b := decodeMiss(t, responseText(resp))
		require.False(t, b.Found)
		require.Equal(t, &no, b.Available, "the answer must be `unavailable`, not a plain miss")
		require.NotEmpty(t, b.Reason)
		require.Empty(t, b.Searched, "an unavailable answer is not a report of where it looked")
	})

	t.Run("by bare chunk hash", func(t *testing.T) {
		f, _, _, id := spanAuthObject(t)
		root := spanRootOf(t, f, mustToolUseRoot(t, f, id))
		chunk := root.Chunks[0].Hash
		damageObject(t, f, chunk)

		resp := f.call(t, ToolExpand, map[string]any{"hash": chunk.String(), "full": true})
		require.False(t, resp.IsError, "%s", responseText(resp))

		b := decodeMiss(t, responseText(resp))
		require.False(t, b.Found)
		require.Equal(t, &no, b.Available,
			"the chunk-hash courtesy branch must not report a refused object as ABSENT")
		require.NotEmpty(t, b.Reason)
	})
}

// TestReReadReportsADamagedObjectAsUnavailable is the same finding through the path address form.
func TestReReadReportsADamagedObjectAsUnavailable(t *testing.T) {
	f, _, root, _ := spanAuthObject(t)
	damageObject(t, f, root.Chunks[0].Hash)

	resp := f.call(t, ToolReRead, map[string]any{"path": spanAuthPath, "full": true})
	require.False(t, resp.IsError, "%s", responseText(resp))

	b := decodeMiss(t, responseText(resp))
	require.False(t, b.Found)
	no := false
	require.Equal(t, &no, b.Available)
}

// TestDamagedAnswerCarriesNoAddress: the refusal must not echo a hash, a path or anything else the
// caller could mine. The store has already Louded the full identity where an operator can act on it.
func TestDamagedAnswerCarriesNoAddress(t *testing.T) {
	f, _, root, id := spanAuthObject(t)
	chunk := root.Chunks[0].Hash
	damageObject(t, f, chunk)

	for _, args := range []map[string]any{
		{"tool_use_id": string(id), "full": true},
		{"hash": chunk.String(), "full": true},
	} {
		text := responseText(f.call(t, ToolExpand, args))
		require.NotContains(t, text, chunk.String(), "the refusal must not echo the object address")
		require.NotContains(t, text, spanAuthPath, "the refusal must not echo the path")
	}
}

// mustToolUseRoot reads the root hash a tool-use record points at.
func mustToolUseRoot(t *testing.T, f *fixture, id core.ToolUseID) core.Hash {
	t.Helper()
	rec, err := f.Store.ToolUse(context.Background(), id)
	require.NoError(t, err)
	return rec.Root
}

// TestAuthorizePathRefusesAResolutionOutsideTheRoot is finding S-1.
//
// paths.Norm is the store-key normaliser: when EvalSymlinks lands outside the root it DISCARDS the
// resolution and keeps the unresolved spelling, which is a deliberate anti-smuggling rule. So a
// captured file whose parent directory has since been replaced by a link pointing out of the project
// normalises cleanly, and an authorization gate built on Norm alone has nothing to refuse — the
// archive served content for an address a live read would be denied today.
//
// The link is a real symlink where privilege permits and an NTFS junction otherwise, because a
// directory symlink on Windows needs Developer Mode or SeCreateSymbolicLinkPrivilege while a
// junction needs neither — and a junction is the shape a directory swap actually takes there. The
// test skips, loudly, if neither can be created: a silent pass would be measuring nothing.
func TestAuthorizePathRefusesAResolutionOutsideTheRoot(t *testing.T) {
	f, _, _, id := spanAuthObject(t)

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "auth.ts"), []byte("outside\n"), 0o600))

	dir := filepath.Dir(filepath.Join(f.Root, filepath.FromSlash(spanAuthPath)))
	require.NoError(t, os.RemoveAll(paths.Long(dir)))
	if err := makeDirLink(dir, outside); err != nil {
		t.Skip("platform: this host will create neither a directory symlink nor a junction (" +
			runtime.GOOS + "): " + err.Error())
	}
	t.Cleanup(func() { _ = os.Remove(paths.Long(dir)) })

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{ToolExpand, map[string]any{"tool_use_id": string(id), "full": true}},
		{ToolReRead, map[string]any{"path": spanAuthPath, "full": true}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			text := responseText(f.call(t, tc.tool, tc.args))
			require.NotContains(t, text, `"found":true`,
				"%s served content for an escaping address: %s", tc.tool, text)

			var d deniedBody
			require.NoError(t, json.Unmarshal([]byte(text), &d), "body was not JSON: %s", text)
			require.True(t, d.Denied, "%s must refuse explicitly, not merely miss: %s", tc.tool, text)
			require.False(t, d.Found)
			require.NotContains(t, d.Reason, spanAuthPath, "the refusal must not echo the path")
			require.NotContains(t, d.Reason, outside, "the refusal must not name what is outside")
		})
	}
}

// TestAuthorizePathAllowsAnOrdinaryPath keeps S-1's gate from refusing everything: the same fixture
// with no link at all must still serve.
func TestAuthorizePathAllowsAnOrdinaryPath(t *testing.T) {
	f, _, _, id := spanAuthObject(t)
	body := spanContentOf(t, f, ToolExpand, map[string]any{"tool_use_id": string(id)})
	require.True(t, body.Found, "an ordinary in-project path must still resolve")
}

// TestAuthorizePathAllowsADeletedFile: retrieval is HISTORICAL, so a path whose file no longer
// exists is still authorized as long as the components that DO exist keep it inside the root.
func TestAuthorizePathAllowsADeletedFile(t *testing.T) {
	f, _, _, id := spanAuthObject(t)
	// The fixture stores the object without ever writing the working-tree file, so the path is
	// already absent on disk. Removing its parent directory too makes the "nothing on this path
	// exists below the root" case explicit rather than incidental.
	require.NoError(t, os.RemoveAll(paths.Long(
		filepath.Dir(filepath.Join(f.Root, filepath.FromSlash(spanAuthPath))))))

	body := spanContentOf(t, f, ToolExpand, map[string]any{"tool_use_id": string(id)})
	require.True(t, body.Found, "a deleted file still has archived history")
}
