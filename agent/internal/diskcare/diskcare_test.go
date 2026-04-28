package diskcare

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
)

type fakeEngine struct {
	managed      []engine.ContainerState
	containers   []engine.ContainerState
	images       []engine.ImageState
	logPaths     map[string]string
	removed      []string
	removeErrors map[string]error
}

func (f *fakeEngine) ListManaged(context.Context) ([]engine.ContainerState, error) {
	return append([]engine.ContainerState(nil), f.managed...), nil
}

func (f *fakeEngine) ListContainers(context.Context) ([]engine.ContainerState, error) {
	return append([]engine.ContainerState(nil), f.containers...), nil
}

func (f *fakeEngine) ListImages(context.Context) ([]engine.ImageState, error) {
	return append([]engine.ImageState(nil), f.images...), nil
}

func (f *fakeEngine) ImageExists(_ context.Context, image string) (bool, error) {
	for _, item := range f.images {
		for _, ref := range append(append([]string{item.ID}, item.RepoTags...), item.RepoDigests...) {
			if ref == image {
				return true, nil
			}
		}
	}
	return false, nil
}

func (f *fakeEngine) RemoveImage(_ context.Context, reference string) ([]engine.ImageDelete, error) {
	if err := f.removeErrors[reference]; err != nil {
		return nil, err
	}
	f.removed = append(f.removed, reference)
	return []engine.ImageDelete{{Untagged: reference}}, nil
}

func (f *fakeEngine) Inspect(_ context.Context, name string) (engine.ContainerInfo, error) {
	return engine.ContainerInfo{Name: name, LogPath: f.logPaths[name]}, nil
}

func TestRunRemovesOnlyImagesOutsideRetentionWindow(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "disk-care-state.json")
	now := time.Now().Add(-time.Hour).UTC()
	initial := &store{Releases: []releaseRecord{
		{Environment: "production", Revision: "rev-1", Images: []string{"app:rev1"}, LastSeenAt: now.Add(1 * time.Minute)},
		{Environment: "production", Revision: "rev-2", Images: []string{"app:rev2"}, LastSeenAt: now.Add(2 * time.Minute)},
		{Environment: "production", Revision: "rev-3", Images: []string{"app:rev3"}, LastSeenAt: now.Add(3 * time.Minute)},
	}}
	eng := &fakeEngine{
		images: []engine.ImageState{
			{ID: "sha256:1", RepoTags: []string{"app:rev1"}, Size: 100},
			{ID: "sha256:2", RepoTags: []string{"app:rev2"}, Size: 200},
			{ID: "sha256:3", RepoTags: []string{"app:rev3"}, Size: 300},
			{ID: "sha256:envoy", RepoTags: []string{"envoy:latest"}, Size: 400},
		},
		containers: []engine.ContainerState{{Name: "web", Image: "app:rev3", Managed: true}},
		managed:    []engine.ContainerState{{Name: "web", Image: "app:rev3", Managed: true}},
		logPaths:   map[string]string{},
	}
	mgr := New(eng, Config{StatePath: statePath, RetainedPreviousReleases: 1, ProtectedImages: []string{"envoy:latest"}, ContainerLogMaxSize: "10m", ContainerLogMaxFile: 5}, nil)
	if err := mgr.saveStore(initial); err != nil {
		t.Fatalf("save store: %v", err)
	}

	status, err := mgr.Run(context.Background(), desiredState("rev-3", "app:rev3"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(eng.removed) != 1 || eng.removed[0] != "app:rev1" {
		t.Fatalf("removed = %#v, want app:rev1", eng.removed)
	}
	if status.ReclaimedBytes != 100 {
		t.Fatalf("reclaimed bytes = %d, want 100", status.ReclaimedBytes)
	}
	if status.RetainedReleaseCount != 2 {
		t.Fatalf("retained release count = %d, want 2", status.RetainedReleaseCount)
	}
}

func TestRunProtectsImagesUsedByAnyContainer(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "disk-care-state.json")
	initial := &store{Releases: []releaseRecord{
		{Environment: "production", Revision: "rev-1", Images: []string{"app:rev1"}, LastSeenAt: time.Now().Add(-time.Hour)},
		{Environment: "production", Revision: "rev-2", Images: []string{"app:rev2"}, LastSeenAt: time.Now()},
	}}
	eng := &fakeEngine{
		images:     []engine.ImageState{{ID: "sha256:1", RepoTags: []string{"app:rev1"}, Size: 100}, {ID: "sha256:2", RepoTags: []string{"app:rev2"}, Size: 200}},
		containers: []engine.ContainerState{{Name: "user", Image: "app:rev1", Managed: false}},
		logPaths:   map[string]string{},
	}
	mgr := New(eng, Config{StatePath: statePath, RetainedPreviousReleases: 0}, nil)
	if err := mgr.saveStore(initial); err != nil {
		t.Fatalf("save store: %v", err)
	}

	_, err := mgr.Run(context.Background(), desiredState("rev-2", "app:rev2"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(eng.removed) != 0 {
		t.Fatalf("removed = %#v, want none", eng.removed)
	}
}

func TestRunReportsManagedDockerLogUsage(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "container-json.log")
	if err := os.WriteFile(logPath, []byte("hello logs"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	eng := &fakeEngine{
		managed:    []engine.ContainerState{{Name: "web", Image: "app:rev1", Managed: true}},
		containers: []engine.ContainerState{{Name: "web", Image: "app:rev1", Managed: true}},
		images:     []engine.ImageState{{ID: "sha256:1", RepoTags: []string{"app:rev1"}, Size: 100}},
		logPaths:   map[string]string{"web": logPath},
	}
	mgr := New(eng, Config{StatePath: filepath.Join(dir, "state.json"), RetainedPreviousReleases: 10, ContainerLogMaxSize: "10m", ContainerLogMaxFile: 5}, nil)

	status, err := mgr.Run(context.Background(), desiredState("rev-1", "app:rev1"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if status.DockerLogBytes != int64(len("hello logs")) {
		t.Fatalf("docker log bytes = %d", status.DockerLogBytes)
	}
	if status.LogMaxSize != "10m" || status.LogMaxFile != 5 {
		t.Fatalf("unexpected log config in status: %#v", status)
	}
}

func desiredState(revision string, image string) *desiredstatepb.DesiredState {
	return &desiredstatepb.DesiredState{
		SchemaVersion: 2,
		Revision:      revision,
		Environments: []*desiredstatepb.Environment{{
			Name:     "production",
			Revision: revision,
			Services: []*desiredstatepb.Service{{
				Name:  "web",
				Kind:  "web",
				Image: image,
			}},
		}},
	}
}
