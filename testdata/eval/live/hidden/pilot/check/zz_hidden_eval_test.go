package check

import (
	"os"
	"strings"
	"testing"
)

func TestHiddenEvalCodeFile(t *testing.T) {
	raw, err := os.ReadFile("../CODE.txt")
	if err != nil || strings.TrimSpace(string(raw)) != "2c1a7a62c3" {
		t.Errorf("CODE.txt = %q, %v; want the code word", raw, err)
	}
}
