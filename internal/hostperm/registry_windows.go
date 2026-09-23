//go:build windows

package hostperm

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

// The registry is read through the standard library's own syscall bindings, so this file adds no
// dependency: §2.5 names golang.org/x/sys/windows but not its registry subpackage.

// Registry value types a settings document may be stored as (winnt.h).
const (
	regSZ       = 1 // REG_SZ
	regExpandSZ = 2 // REG_EXPAND_SZ
)

// hives maps the two policy hives to their predefined handles.
var hives = map[string]syscall.Handle{
	"HKLM": syscall.HKEY_LOCAL_MACHINE,
	"HKCU": syscall.HKEY_CURRENT_USER,
}

// openPolicyKey opens r's key for reading, reporting (0, nil) when it does not exist.
func openPolicyKey(r RegistryValue) (syscall.Handle, error) {
	hive, ok := hives[r.Hive]
	if !ok {
		return 0, fmt.Errorf("unknown registry hive %q", r.Hive)
	}
	name, err := syscall.UTF16PtrFromString(r.Key)
	if err != nil {
		return 0, err
	}
	var k syscall.Handle
	err = syscall.RegOpenKeyEx(hive, name, 0, syscall.KEY_READ, &k)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) || errors.Is(err, syscall.ERROR_PATH_NOT_FOUND) {
		return 0, nil
	}
	return k, err
}

// signRegistry reports r's presence, size and the key's last write time, which Windows advances on
// every change to any of the key's values.
func signRegistry(r RegistryValue) signature {
	k, err := openPolicyKey(r)
	if err != nil {
		return signature{id: r.id(), errText: err.Error()}
	}
	if k == 0 {
		return signature{id: r.id()}
	}
	defer func() { _ = syscall.RegCloseKey(k) }()
	var ft syscall.Filetime
	if err := syscall.RegQueryInfoKey(k, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, &ft); err != nil {
		return signature{id: r.id(), errText: err.Error()}
	}
	name, err := syscall.UTF16PtrFromString(r.Name)
	if err != nil {
		return signature{id: r.id(), errText: err.Error()}
	}
	var typ, n uint32
	err = syscall.RegQueryValueEx(k, name, nil, &typ, nil, &n)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return signature{id: r.id()}
	}
	if err != nil {
		return signature{id: r.id(), errText: err.Error()}
	}
	return signature{id: r.id(), present: true, size: int64(n), mod: ft.Nanoseconds()}
}

// readRegistry returns r's settings document as UTF-8, expanding %VARIABLES% in a REG_EXPAND_SZ.
func readRegistry(r RegistryValue, getenv func(string) string) ([]byte, error) {
	k, err := openPolicyKey(r)
	if err != nil {
		return nil, err
	}
	if k == 0 {
		return nil, errors.New("the registry key disappeared while it was being read")
	}
	defer func() { _ = syscall.RegCloseKey(k) }()
	name, err := syscall.UTF16PtrFromString(r.Name)
	if err != nil {
		return nil, err
	}
	var typ, n uint32
	if err := syscall.RegQueryValueEx(k, name, nil, &typ, nil, &n); err != nil {
		return nil, err
	}
	if typ != regSZ && typ != regExpandSZ {
		return nil, fmt.Errorf("registry value type %d is not a string", typ)
	}
	if n > maxSettingsBytes*2 {
		return nil, fmt.Errorf("registry value larger than %d bytes", maxSettingsBytes*2)
	}
	const u16 = 2
	buf := make([]uint16, n/u16+1)
	size := uint32(len(buf) * u16)
	if err := syscall.RegQueryValueEx(k, name, nil, &typ, (*byte)(unsafe.Pointer(&buf[0])), &size); err != nil {
		return nil, err
	}
	s := syscall.UTF16ToString(buf)
	if typ == regExpandSZ {
		s = expandPercent(s, getenv)
	}
	return []byte(s), nil
}

// expandPercent expands %NAME% references the way REG_EXPAND_SZ does, leaving an unknown or
// unterminated reference as written.
func expandPercent(s string, getenv func(string) string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '%')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		j := strings.IndexByte(s[i+1:], '%')
		if j < 0 {
			b.WriteString(s)
			return b.String()
		}
		name := s[i+1 : i+1+j]
		b.WriteString(s[:i])
		if v := getenv(name); name != "" && v != "" {
			b.WriteString(v)
		} else {
			b.WriteString(s[i : i+j+2])
		}
		s = s[i+j+2:]
	}
}
