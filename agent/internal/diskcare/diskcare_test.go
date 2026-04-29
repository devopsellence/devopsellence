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
	managed       []engine.ContainerState
	containers    []engine.ContainerState
	images        []engine.ImageState
	logPaths      map[string]string
	removed       []string
	removeErrors  map[string]error
	removeResults map[string][]engine.ImageDelete
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
	if result := f.removeResults[reference]; len(result) > 0 {
		return result, nil
	}
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
		containers:    []engine.ContainerState{{Name: "web", Image: "app:rev3", Managed: true}},
		managed:       []engine.ContainerState{{Name: "web", Image: "app:rev3", Managed: true}},
		logPaths:      map[string]string{},
		removeResults: map[string][]engine.ImageDelete{"app:rev1": {{Deleted: "sha256:1"}}},
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

func TestRunIgnoresCorruptState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "disk-care-state.json")
	if err := os.WriteFile(statePath, []byte("{"), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	eng := &fakeEngine{
		containers: []engine.ContainerState{{Name: "web", Image: "app:rev1", Managed: true}},
		managed:    []engine.ContainerState{{Name: "web", Image: "app:rev1", Managed: true}},
		images:     []engine.ImageState{{ID: "sha256:1", RepoTags: []string{"app:rev1"}, Size: 100}},
		logPaths:   map[string]string{},
	}
	mgr := New(eng, Config{StatePath: statePath, RetainedPreviousReleases: 10}, nil)

	status, err := mgr.Run(context.Background(), desiredState("rev-1", "app:rev1"))
	if err != nil {
		t.Fatalf("run with corrupt state: %v", err)
	}
	if status.LastError == "" {
		t.Fatal("expected corrupt state warning in status")
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read rewritten state: %v", err)
	}
	if string(data) == "{" {
		t.Fatal("expected corrupt state to be replaced")
	}
}

func TestSaveStoreUsesPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	mgr := New(nil, Config{StatePath: filepath.Join(dir, "disk-care-state.json")}, nil)
	if err := mgr.saveStore(&store{}); err != nil {
		t.Fatalf("save store: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat state dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("state dir mode = %o, want 700", got)
	}
}

func TestRunReportsManagedDockerLogUsage(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "container-json.log")
	if err := os.WriteFile(logPath, []byte("hello logs"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if err := os.WriteFile(logPath+".1", []byte("rotated"), 0o600); err != nil {
		t.Fatalf("write rotated log: %v", err)
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
	if status.DockerLogBytes != int64(len("hello logs")+len("rotated")) {
		t.Fatalf("docker log bytes = %d", status.DockerLogBytes)
	}
	if status.LogMaxSize != "10m" || status.LogMaxFile != 5 {
		t.Fatalf("unexpected log config in status: %#v", status)
	}
}

func TestRetainedReleaseKeysNormalizesEnvironmentNames(t *testing.T) {
	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	releases := []releaseRecord{
		{Environment: "production ", Revision: "rev-1", Images: []string{"app:rev1"}, LastSeenAt: older},
		{Environment: "production", Revision: "rev-2", Images: []string{"app:rev2"}, LastSeenAt: newer},
	}
	current := []releaseRecord{{Environment: " production ", Revision: "rev-2", Images: []string{"app:rev2"}, LastSeenAt: newer}}

	retained := retainedReleaseKeys(releases, current, 1)
	if _, ok := retained[releaseKey("production", "rev-2")]; !ok {
		t.Fatal("expected newest production release to be retained")
	}
	if _, ok := retained[releaseKey("production", "rev-1")]; ok {
		t.Fatal("expected older whitespace-variant production release to fall outside retention")
	}
}

func TestRetainedReleaseKeysPrunesOlderAbsentEnvironmentReleases(t *testing.T) {
	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	releases := []releaseRecord{
		{Environment: "deleted", Revision: "rev-1", Images: []string{"app:rev1"}, LastSeenAt: older},
		{Environment: "deleted", Revision: "rev-2", Images: []string{"app:rev2"}, LastSeenAt: newer},
	}

	retained := retainedReleaseKeys(releases, nil, 1)
	if _, ok := retained[releaseKey("deleted", "rev-2")]; !ok {
		t.Fatal("expected newest absent-environment release to stay inside retention")
	}
	if _, ok := retained[releaseKey("deleted", "rev-1")]; ok {
		t.Fatal("expected older absent-environment release to fall outside retention")
	}
}

func TestRunRetainsDetachedEnvironmentImagesInsideRetentionWindow(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "disk-care-state.json")
	initial := &store{Releases: []releaseRecord{
		{Environment: "production", Revision: "rev-1", Images: []string{"app:rev1"}, LastSeenAt: time.Now().Add(-time.Minute)},
	}}
	eng := &fakeEngine{
		images:        []engine.ImageState{{ID: "sha256:1", RepoTags: []string{"app:rev1"}, Size: 100}},
		containers:    []engine.ContainerState{},
		logPaths:      map[string]string{},
		removeResults: map[string][]engine.ImageDelete{"app:rev1": {{Deleted: "sha256:1"}}},
	}
	mgr := New(eng, Config{StatePath: statePath, RetainedPreviousReleases: 1}, nil)
	if err := mgr.saveStore(initial); err != nil {
		t.Fatalf("save store: %v", err)
	}

	_, err := mgr.Run(context.Background(), &desiredstatepb.DesiredState{SchemaVersion: 2, Revision: "empty"})
	if err != nil {
		t.Fatalf("run detached state: %v", err)
	}
	if len(eng.removed) != 0 {
		t.Fatalf("removed = %#v, want none; detached environments must retain recent images so reattach + same-revision deploy can recover", eng.removed)
	}
}

func TestStoreNormalizeCanonicalizesPersistedReleaseRecords(t *testing.T) {
	now := time.Now()
	s := &store{Releases: []releaseRecord{
		{Environment: " production ", Revision: " rev-1", Images: []string{" app:rev1 ", "app:rev1"}, LastSeenAt: now.Add(-time.Minute)},
		{Environment: "production", Revision: "rev-1", Images: []string{"app:rev1b"}, LastSeenAt: now},
	}}

	s.normalize()
	if len(s.Releases) != 1 {
		t.Fatalf("releases = %#v, want one normalized record", s.Releases)
	}
	got := s.Releases[0]
	if got.Environment != "production" || got.Revision != "rev-1" {
		t.Fatalf("normalized release = %#v", got)
	}
	if len(got.Images) != 2 || got.Images[0] != "app:rev1" || got.Images[1] != "app:rev1b" {
		t.Fatalf("normalized images = %#v", got.Images)
	}
	if !got.LastSeenAt.Equal(now) {
		t.Fatalf("last seen = %v, want %v", got.LastSeenAt, now)
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
