package store

import (
	"encoding/json"
	"errors"
	"io"
)

// Load reads what Save writes.
func Load(r io.Reader) ([]Entry, error) {
	dec := json.NewDecoder(r)
	var out []Entry
	for {
		var e Entry
		err := dec.Decode(&e)
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
}
