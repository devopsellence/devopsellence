package noop

import (
	"context"

	"github.com/devopsellence/devopsellence/agent/internal/report"
)

type Reporter struct{}

func New() *Reporter {
	return &Reporter{}
}

func (r *Reporter) Report(ctx context.Context, status report.Status) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}
