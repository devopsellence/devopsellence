package diagnose

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeRequestClient struct {
	claimCalls int
	request    *Request
	claimErr   error
}

func (f *fakeRequestClient) Claim(context.Context) (*Request, error) {
	f.claimCalls++
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	return f.request, nil
}

func (f *fakeRequestClient) Complete(context.Context, int, Result) error {
	return nil
}

func (f *fakeRequestClient) Fail(context.Context, int, string) error {
	return nil
}

type fakeCollector struct{}

func (fakeCollector) Collect(context.Context) (Result, error) {
	return Result{}, nil
}

func TestRunnerPollsOncePerDesiredStateSequence(t *testing.T) {
	t.Parallel()

	client := &fakeRequestClient{}
	runner := NewRunner(client, fakeCollector{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := runner.RunOnce(context.Background(), 5); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if err := runner.RunOnce(context.Background(), 5); err != nil {
		t.Fatalf("RunOnce() repeat error = %v", err)
	}
	if err := runner.RunOnce(context.Background(), 6); err != nil {
		t.Fatalf("RunOnce() next sequence error = %v", err)
	}

	if client.claimCalls != 2 {
		t.Fatalf("claim calls = %d, want 2", client.claimCalls)
	}
}

func TestRunnerFallsBackToSlowPollingWithoutDesiredStateSignal(t *testing.T) {
	t.Parallel()

	client := &fakeRequestClient{}
	runner := NewRunner(client, fakeCollector{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	runner.now = func() time.Time { return now }

	if err := runner.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce() initial error = %v", err)
	}
	now = now.Add(10 * time.Second)
	if err := runner.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce() throttled error = %v", err)
	}
	now = now.Add(25 * time.Second)
	if err := runner.RunOnce(context.Background(), 0); err != nil {
		t.Fatalf("RunOnce() fallback error = %v", err)
	}

	if client.claimCalls != 2 {
		t.Fatalf("claim calls = %d, want 2", client.claimCalls)
	}
}

func TestRunnerRetriesSignalPollAfterClaimFailure(t *testing.T) {
	t.Parallel()

	client := &fakeRequestClient{claimErr: errors.New("boom")}
	runner := NewRunner(client, fakeCollector{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if err := runner.RunOnce(context.Background(), 7); err == nil {
		t.Fatal("expected first RunOnce() to fail")
	}
	if err := runner.RunOnce(context.Background(), 7); err == nil {
		t.Fatal("expected second RunOnce() to fail")
	}

	if client.claimCalls != 2 {
		t.Fatalf("claim calls = %d, want 2", client.claimCalls)
	}
}
