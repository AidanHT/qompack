package chunk

import (
	"fmt"

	"github.com/qompack/qompack/internal/config"
)

// Validate reports whether p's boundaries are usable: 0 < Min < Target < Max
// (00-ARCHITECTURE.md §5.5; internal/config's own store.chunk.{min,target,max} range tags agree —
// see ChunkCfg in internal/config/config.go). Validate is fully specified by the architecture, so
// SP-01 implements it for real rather than stubbing it (00-ARCHITECTURE.md §14.1): every
// constructor downstream of a loaded config.Config needs a working check before SP-04's real
// Chunker exists.
func (p Params) Validate() error {
	if p.Min <= 0 {
		return fmt.Errorf("chunk: Params.Validate: min must be positive, got %d", p.Min)
	}
	if p.Target <= p.Min {
		return fmt.Errorf("chunk: Params.Validate: target (%d) must be greater than min (%d)", p.Target, p.Min)
	}
	if p.Max <= p.Target {
		return fmt.Errorf("chunk: Params.Validate: max (%d) must be greater than target (%d)", p.Max, p.Target)
	}
	return nil
}

// DefaultParams returns the FastCDC parameters from config.Defaults().Store.Chunk. It is fully
// specified by the architecture, so SP-01 implements it for real rather than stubbing it
// (00-ARCHITECTURE.md §14.1).
//
// §5.5 gives DefaultParams no arguments, so — unlike New(p Params), which the daemon uses to
// construct a Chunker from whatever config.Config a project actually loaded — DefaultParams
// always reads the built-in defaults, never a loaded project config. Callers that already hold a
// config.Config should read its Store.Chunk directly and pass the result to New; DefaultParams
// exists for callers (tests, tools, a bare chunk.New(chunk.DefaultParams())) that do not.
func DefaultParams() Params {
	c := config.Defaults().Store.Chunk
	return Params{Min: c.Min, Target: c.Target, Max: c.Max}
}
