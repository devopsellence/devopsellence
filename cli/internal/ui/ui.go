package ui

import (
	"context"
	"io"
)

// RunTask executes fn directly. Agent-primary CLI output is structured by the
// workflow layer, not animated progress output.
func RunTask(ctx context.Context, _ io.Writer, _ string, fn func(context.Context, func(string), func(string)) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	noop := func(string) {}
	return fn(ctx, noop, noop)
}
