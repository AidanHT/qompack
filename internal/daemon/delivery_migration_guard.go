package daemon

import (
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/paths"
)

// A missing legacy pair is fresh only when no migration evidence survives.
// Recreating it over archived generations would reuse arrival sequences and
// observation identities. This guard enables no rollover and does not claim
// that an older executable will enforce the same refusal.
func refuseDeliveryRecreation(state string) error {
	for _, name := range []string{
		"delivery-generations", "delivery-journal.json", "delivery-segments", "delivery-terminal",
	} {
		_, err := os.Lstat(paths.Long(filepath.Join(state, name)))
		if !os.IsNotExist(err) {
			return deliveryJournalError()
		}
	}
	return nil
}
