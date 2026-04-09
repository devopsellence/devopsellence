package diagnose

import (
	"context"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/engine"
)

type fakeEngine struct {
	states []engine.ContainerState
	info   map[string]engine.ContainerInfo
	logs   map[string][]byte
}

func (f *fakeEngine) ListManaged(ctx context.Context) ([]engine.ContainerState, error) {
	return f.states, nil
}

func (f *fakeEngine) CreateAndStart(ctx context.Context, spec engine.ContainerSpec) error {
	return nil
}

func (f *fakeEngine) Start(ctx context.Context, name string) error {
	return nil
}

func (f *fakeEngine) Wait(ctx context.Context, name string) (int64, error) {
	return 0, nil
}

func (f *fakeEngine) Stop(ctx context.Context, name string, timeout time.Duration) error {
	return nil
}

func (f *fakeEngine) Remove(ctx context.Context, name string) error {
	return nil
}

func (f *fakeEngine) ImageExists(ctx context.Context, image string) (bool, error) {
	return true, nil
}

func (f *fakeEngine) PullImage(ctx context.Context, image string, auth *engine.RegistryAuth) error {
	return nil
}

func (f *fakeEngine) Inspect(ctx context.Context, name string) (engine.ContainerInfo, error) {
	return f.info[name], nil
}

func (f *fakeEngine) EnsureNetwork(ctx context.Context, name string) error {
	return nil
}

func (f *fakeEngine) Logs(ctx context.Context, name string, tail int) ([]byte, error) {
	return f.logs[name], nil
}

func TestCollectorIncludesLogTailForStoppedContainer(t *testing.T) {
	t.Parallel()

	eng := &fakeEngine{
		states: []engine.ContainerState{
			{Name: "devopsellence-web", Service: "web", Image: "shop-app", Hash: "hash-1"},
		},
		info: map[string]engine.ContainerInfo{
			"devopsellence-web": {
				Name:           "devopsellence-web",
				Running:        false,
				HasHealthcheck: true,
				Health:         "unhealthy",
				NetworkIP:      map[string]string{"devopsellence": "172.18.0.10"},
			},
		},
		logs: map[string][]byte{
			"devopsellence-web": []byte("boot failed\n"),
		},
	}

	collector := NewCollector(eng)
	collector.now = func() time.Time { return time.Date(2026, 3, 29, 20, 0, 0, 0, time.UTC) }

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if result.Summary.Status != "error" {
		t.Fatalf("status = %q, want error", result.Summary.Status)
	}
	if result.Summary.LogsIncluded != 1 {
		t.Fatalf("logs included = %d, want 1", result.Summary.LogsIncluded)
	}
	if got := result.Containers[0].LogTail; got != "boot failed" {
		t.Fatalf("log tail = %q", got)
	}
}
