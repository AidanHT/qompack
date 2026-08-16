package cli

// The eleven fault-injection sites of task-6-spec.md's table, verbatim. Declared in an
// unconstrained file (no //go:build tag) so both the default build (fault.go) and the -tags
// noinject build (fault_noinject.go) share exactly one spelling of each site name — the thing
// TestFaultSitesInertWhenUnset's byte-identical-output assertion depends on actually being true of
// the same binary surface, not two independently typed copies that could drift.
const (
	faultStdinEOF      = "stdin-eof"
	faultStdinGarbage  = "stdin-garbage"
	faultOversize      = "oversize"
	faultDaemonDown    = "daemon-down"
	faultSpoolReadonly = "spool-readonly"
	faultSpoolFull     = "spool-full"
	faultDiskFull      = "disk-full"
	faultStateCorrupt  = "state-corrupt"
	faultConfigCorrupt = "config-corrupt"
	faultPanicHook     = "panic:hook"
	faultPanicClient   = "panic:client"
)

// allFaultSites is every site above, in table order — used by fault.go's parser (to recognize a
// bare site token) and by tests that must exercise "all eleven" without the list quietly losing
// one.
var allFaultSites = []string{
	faultStdinEOF, faultStdinGarbage, faultOversize, faultDaemonDown, faultSpoolReadonly,
	faultSpoolFull, faultDiskFull, faultStateCorrupt, faultConfigCorrupt, faultPanicHook, faultPanicClient,
}

// noopSpawn is the ClientOptions.Spawn / daemon-starter substitute the "daemon-down" fault site
// installs: task-6-spec.md's table requires that site to leave "ClientOptions.Spawn ... set to a
// no-op" so a fault run never launches a real detached daemon process. It carries no
// fault-injection logic of its own — just the shape daemon.SpawnDetached has — so it lives outside
// both build-tagged fault files and needs no noinject counterpart.
func noopSpawn(string, string) error { return nil }
