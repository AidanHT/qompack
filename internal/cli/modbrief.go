package cli

import (
	"context"
	"errors"
	"io"

	"github.com/qompack/qompack/internal/modwire"
)

// modBrief is `qompack mod brief`.
func modBrief(_ context.Context, _ Env, _ io.Reader) any {
	return modwire.FailureFor(errors.New("not implemented"))
}
