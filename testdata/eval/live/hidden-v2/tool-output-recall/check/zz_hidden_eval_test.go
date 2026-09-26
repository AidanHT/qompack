package check

import (
	"os"
	"strings"
	"testing"
)

func TestHiddenEvalTokenFile(t *testing.T) {
	raw, err := os.ReadFile("../TOKEN")
	if err != nil || strings.TrimSpace(string(raw)) != "975408daf9b5" {
		t.Errorf("TOKEN = %q, %v; want the build token the probe printed, alone on one line", raw, err)
	}
}
