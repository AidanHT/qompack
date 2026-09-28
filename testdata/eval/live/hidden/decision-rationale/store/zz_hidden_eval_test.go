package store_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"example.com/store/store"
)

func TestHiddenEvalSaveIsJSONLines(t *testing.T) {
	in := []store.Entry{{Key: "a", Value: "1", TTL: 5}, {Key: "b", Value: "two words", TTL: 0}}
	var buf bytes.Buffer
	if err := store.Save(&buf, in); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != len(in) {
		t.Fatalf("Save wrote %d line(s) for %d entries; want one JSON object per line", len(lines), len(in))
	}
	for i, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil || m["key"] != in[i].Key {
			t.Errorf("line %d = %q is not the JSON object for entry %q", i, l, in[i].Key)
		}
	}
}

func TestHiddenEvalRoundTrip(t *testing.T) {
	in := []store.Entry{{Key: "k", Value: "v", TTL: 60}, {Key: "k2", Value: "", TTL: 1}}
	var buf bytes.Buffer
	if err := store.Save(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := store.Load(&buf)
	if err != nil || !reflect.DeepEqual(out, in) {
		t.Errorf("Load(Save(x)) = %v, %v; want %v", out, err, in)
	}
}
