//go:build windows

package pathstest

import (
	"errors"
	"runtime"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// accessBypassPrivileges are the privileges that let a token past a file's access-control list:
// SeBackupPrivilege grants any read and SeRestorePrivilege any write, to an open that asks for
// backup semantics. That is not an exotic open. Go's os.Open passes FILE_FLAG_BACKUP_SEMANTICS for
// every read-only open (so that it can open a directory), internal/paths.OpenShared does too, and
// the I/O manager opens a rename's target directory with backup intent. An elevated account whose
// token has the two enabled, as the hosted Windows runner's has, therefore reads a file whose ACL
// denies it and renames into a directory whose ACL denies it, while a plain create there is still
// refused (nightly 36820740318: the deny-ACE rows of internal/daemon and internal/paths).
var accessBypassPrivileges = []string{"SeBackupPrivilege", "SeRestorePrivilege"}

// WithoutBackupPrivileges runs fn on one OS thread whose token is a copy of the process's own with
// accessBypassPrivileges removed, so that an access-control entry a fixture writes is enforced on
// fn's file operations for any account, an elevated one included. fn's file operations must run on
// the calling goroutine, which is locked to the thread for the call; the impersonation ends when fn
// returns, or when a require inside fn ends the test. Nothing outside this thread changes, and a token that never held the privileges, an
// ordinary user's, runs fn unchanged.
func WithoutBackupPrivileges(t testing.TB, fn func()) {
	t.Helper()
	runtime.LockOSThread()
	if err := windows.ImpersonateSelf(windows.SecurityImpersonation); err != nil {
		runtime.UnlockOSThread()
		t.Fatalf("pathstest: ImpersonateSelf: %v", err)
	}
	defer func() {
		if err := windows.RevertToSelf(); err != nil {
			// The thread keeps the reduced token, so it stays locked: Go ends a locked thread with
			// its goroutine instead of handing it back to the scheduler.
			t.Errorf("pathstest: RevertToSelf: %v", err)
			return
		}
		runtime.UnlockOSThread()
	}()
	var tok windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(),
		windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, true, &tok); err != nil {
		t.Fatalf("pathstest: OpenThreadToken: %v", err)
	}
	defer func() { _ = tok.Close() }()
	for _, name := range accessBypassPrivileges {
		removePrivilege(t, tok, name)
	}
	fn()
}

// removePrivilege removes name from tok. A token that does not hold it is already what is wanted:
// AdjustTokenPrivileges then reports ERROR_NOT_ALL_ASSIGNED, which is not a failure here.
func removePrivilege(t testing.TB, tok windows.Token, name string) {
	t.Helper()
	u, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatalf("pathstest: %s: %v", name, err)
	}
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, u, &luid); err != nil {
		t.Fatalf("pathstest: LookupPrivilegeValue(%s): %v", name, err)
	}
	tp := windows.Tokenprivileges{PrivilegeCount: 1}
	tp.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: windows.SE_PRIVILEGE_REMOVED}
	err = windows.AdjustTokenPrivileges(tok, false, &tp, uint32(unsafe.Sizeof(tp)), nil, nil)
	if err != nil && !errors.Is(err, windows.ERROR_NOT_ALL_ASSIGNED) {
		t.Fatalf("pathstest: removing %s: %v", name, err)
	}
}

// EnabledBypassPrivileges returns which of accessBypassPrivileges the calling thread's effective
// token (its impersonation token, or the process's when it has none) holds enabled. A fixture
// reports it so that a hosted run says which kind of account it ran under.
func EnabledBypassPrivileges(t testing.TB) []string {
	t.Helper()
	tok, err := effectiveToken()
	if err != nil {
		t.Fatalf("pathstest: opening the effective token: %v", err)
	}
	defer func() { _ = tok.Close() }()
	var n uint32
	_ = windows.GetTokenInformation(tok, windows.TokenPrivileges, nil, 0, &n)
	if n == 0 {
		t.Fatal("pathstest: GetTokenInformation(TokenPrivileges) reported no size")
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(tok, windows.TokenPrivileges, &buf[0], n, &n); err != nil {
		t.Fatalf("pathstest: GetTokenInformation(TokenPrivileges): %v", err)
	}
	held := (*windows.Tokenprivileges)(unsafe.Pointer(&buf[0]))
	var out []string
	for _, name := range accessBypassPrivileges {
		u, err := windows.UTF16PtrFromString(name)
		if err != nil {
			t.Fatalf("pathstest: %s: %v", name, err)
		}
		var luid windows.LUID
		if err := windows.LookupPrivilegeValue(nil, u, &luid); err != nil {
			t.Fatalf("pathstest: LookupPrivilegeValue(%s): %v", name, err)
		}
		for _, p := range held.AllPrivileges() {
			if p.Luid == luid && p.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
				out = append(out, name)
			}
		}
	}
	return out
}

// effectiveToken opens the calling thread's impersonation token, or the process token when the
// thread has none.
func effectiveToken() (windows.Token, error) {
	var tok windows.Token
	err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &tok)
	if errors.Is(err, windows.ERROR_NO_TOKEN) {
		err = windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &tok)
	}
	return tok, err
}
