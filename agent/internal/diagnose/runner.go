package diagnose

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const fallbackPollInterval = 30 * time.Second

type Request struct {
	ID          int    `json:"id"`
	RequestedAt string `json:"requested_at"`
}

type RequestClient interface {
	Claim(ctx context.Context) (*Request, error)
	Complete(ctx context.Context, requestID int, result Result) error
	Fail(ctx context.Context, requestID int, message string) error
}

type Runner struct {
	client                   RequestClient
	collector                SnapshotCollector
	logger                   *slog.Logger
	now                      func() time.Time
	lastPollAt               time.Time
	lastDesiredStateSequence int64
}

func NewRunner(client RequestClient, collector SnapshotCollector, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		client:    client,
		collector: collector,
		logger:    logger,
		now:       time.Now,
	}
}

func (r *Runner) RunOnce(ctx context.Context, desiredStateSequence int64) error {
	if r == nil || r.client == nil || r.collector == nil {
		return nil
	}
	if !r.shouldPoll(desiredStateSequence) {
		return nil
	}

	request, err := r.client.Claim(ctx)
	if err != nil {
		return fmt.Errorf("claim diagnose request: %w", err)
	}
	r.recordPoll(desiredStateSequence)
	if request == nil {
		return nil
	}

	result, err := r.collector.Collect(ctx)
	if err != nil {
		if reportErr := r.client.Fail(ctx, request.ID, err.Error()); reportErr != nil {
			return fmt.Errorf("collect diagnose request: %v; report failure: %w", err, reportErr)
		}
		r.logger.Warn("node diagnose failed", "request_id", request.ID, "error", err)
		return err
	}

	if err := r.client.Complete(ctx, request.ID, result); err != nil {
		return fmt.Errorf("complete diagnose request %d: %w", request.ID, err)
	}
	return nil
}

func (r *Runner) shouldPoll(desiredStateSequence int64) bool {
	if desiredStateSequence > 0 {
		return desiredStateSequence != r.lastDesiredStateSequence
	}
	return r.lastPollAt.IsZero() || r.now().Sub(r.lastPollAt) >= fallbackPollInterval
}

func (r *Runner) recordPoll(desiredStateSequence int64) {
	r.lastPollAt = r.now()
	if desiredStateSequence > 0 {
		r.lastDesiredStateSequence = desiredStateSequence
	}
}
