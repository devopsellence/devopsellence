package authority

import (
	"context"
	"errors"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

var ErrNoDesiredState = errors.New("no desired state available")

type FetchResult struct {
	Desired  *desiredstatepb.DesiredState
	Sequence int64
}

type Authority interface {
	Fetch(ctx context.Context) (*FetchResult, error)
}
