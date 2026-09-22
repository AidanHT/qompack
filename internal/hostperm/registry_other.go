//go:build !windows

package hostperm

import "errors"

// errNoRegistry is the answer for a registry source named on a platform without one. It is an
// error rather than "absent" because a caller that named a registry value expected one to exist to
// be read, and silence would turn that expectation into a pass.
var errNoRegistry = errors.New("this platform has no Windows registry")

// signRegistry reports a named registry value as unreadable: there is no registry here.
func signRegistry(r RegistryValue) signature {
	return signature{id: r.id(), errText: errNoRegistry.Error()}
}

// readRegistry is unreachable off Windows: signRegistry never reports a value present.
func readRegistry(RegistryValue, func(string) string) ([]byte, error) {
	return nil, errNoRegistry
}
