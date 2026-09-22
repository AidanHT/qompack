package config_test

import (
	"testing"

	"example.com/svc/config"
)

func TestHiddenEvalRegion(t *testing.T) {
	if got := config.Region(); got != "QX-7731-EU" {
		t.Errorf("Region() = %q, want the region code the session was given", got)
	}
}

func TestHiddenEvalMaxRetries(t *testing.T) {
	if got := config.MaxRetries(); got != 7 {
		t.Errorf("MaxRetries() = %d, want 7 (README.md's retry policy)", got)
	}
}
