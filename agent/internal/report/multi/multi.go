package multi

import (
	"context"
	"errors"

	"github.com/devopsellence/devopsellence/agent/internal/report"
)

type Reporter struct {
	reporters []report.Reporter
}

func New(reporters ...report.Reporter) *Reporter {
	filtered := make([]report.Reporter, 0, len(reporters))
	for _, reporter := range reporters {
		if reporter != nil {
			filtered = append(filtered, reporter)
		}
	}
	return &Reporter{reporters: filtered}
}

func (r *Reporter) Report(ctx context.Context, status report.Status) error {
	var result error
	for _, reporter := range r.reporters {
		if err := reporter.Report(ctx, status); err != nil {
			result = errors.Join(result, err)
		}
	}
	return result
}
