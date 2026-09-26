package config_test

import (
	"testing"

	"example.com/seed/config"
)

// seed compiles only when config.Seed is a string constant, which is what the task asked for.
const seed string = config.Seed

func TestHiddenEvalSeed(t *testing.T) {
	if seed != "5cc87334711f2d40" {
		t.Errorf("config.Seed = %q, want the seed the program printed", seed)
	}
}
