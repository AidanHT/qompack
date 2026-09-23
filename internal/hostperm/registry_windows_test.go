//go:build windows

package hostperm

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The registry tests write under a key of their own in HKCU, never under the real
// SOFTWARE\Policies\ClaudeCode: a Claude Code session on this machine reads that key, and a test must
// not change a live host's policy. reg.exe does the writing because the standard library exposes
// no registry write call.

// testRegistryKey returns a fresh HKCU subkey and removes it when the test ends.
func testRegistryKey(t *testing.T) string {
	t.Helper()
	key := fmt.Sprintf(`SOFTWARE\QompackHostpermTest\%d`, time.Now().UnixNano())
	t.Cleanup(func() {
		_ = exec.Command("reg", "delete", `HKCU\`+key, "/f").Run()
	})
	return key
}

// regAdd writes one value with reg.exe.
func regAdd(t *testing.T, key, typ, data string) {
	t.Helper()
	out, err := exec.Command("reg", "add", `HKCU\`+key, "/v", "Settings", "/t", typ, "/d", data, "/f").CombinedOutput()
	require.NoError(t, err, "reg add: %s", out)
}

func newRegistryEnv(t *testing.T, key string) *diskEnv {
	t.Helper()
	e := newDiskEnv(t)
	e.pol.o.Managed.Registry = []RegistryValue{{Hive: "HKCU", Key: key, Name: "Settings"}}
	return e
}

func TestRegistryPolicyIsRead(t *testing.T) {
	key := testRegistryKey(t)
	e := newRegistryEnv(t, key)
	require.Equal(t, Allow, e.check(t, "a.key"), "an absent key is simply absent")

	regAdd(t, key, "REG_SZ", `{"permissions":{"deny":["Read(*.key)"]}}`)
	require.Equal(t, Deny, e.check(t, "a.key"))

	regAdd(t, key, "REG_EXPAND_SZ", `{"permissions":{"ask":["Read(//%QOMPACK_HOSTPERM_DIR%/**)"]}}`)
	e.vars["QOMPACK_HOSTPERM_DIR"] = "c/vault"
	d, err := e.pol.Check(`C:\vault\x`)
	require.NoError(t, err)
	require.Equal(t, Ask, d.Effect, "an edit to the value is seen, and REG_EXPAND_SZ expands")
}

func TestRegistryPolicyOfTheWrongTypeFailsClosed(t *testing.T) {
	key := testRegistryKey(t)
	e := newRegistryEnv(t, key)
	regAdd(t, key, "REG_DWORD", "1")
	_, err := e.pol.Snapshot()
	require.True(t, errors.Is(err, ErrUnavailable), "%v", err)

	regAdd(t, key, "REG_SZ", `{"permissions":`)
	_, err = e.pol.Snapshot()
	require.True(t, errors.Is(err, ErrUnavailable), "%v", err)
}

func TestUnknownHiveFailsClosed(t *testing.T) {
	e := newDiskEnv(t)
	e.pol.o.Managed.Registry = []RegistryValue{{Hive: "HKXX", Key: "x", Name: "Settings"}}
	_, err := e.pol.Snapshot()
	require.True(t, errors.Is(err, ErrUnavailable), "%v", err)
}

func TestExpandPercent(t *testing.T) {
	get := func(k string) string { return map[string]string{"A": "1", "HOME": filepath.Join("x", "y")}[k] }
	require.Equal(t, "1-"+filepath.Join("x", "y")+"-%B%-%%-%tail", expandPercent("%A%-%HOME%-%B%-%%-%tail", get))
}
