package report

import "context"

type Reporter interface {
	Report(ctx context.Context, status Status) error
}
