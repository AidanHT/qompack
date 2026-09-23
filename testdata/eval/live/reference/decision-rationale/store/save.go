package store

import (
	"encoding/json"
	"io"
)

// Save writes entries as newline-delimited JSON (decision D-17).
func Save(w io.Writer, entries []Entry) error {
	enc := json.NewEncoder(w)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}
