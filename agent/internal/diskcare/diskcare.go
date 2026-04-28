package diskcare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstate"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/engine"
	"github.com/devopsellence/devopsellence/agent/internal/report"
)

const defaultRetainedPreviousReleases = 10

// Engine is the Docker-facing surface used by automatic disk care.
type Engine interface {
	ListManaged(ctx context.Context) ([]engine.ContainerState, error)
	ListContainers(ctx context.Context) ([]engine.ContainerState, error)
	ListImages(ctx context.Context) ([]engine.ImageState, error)
	ImageExists(ctx context.Context, image string) (bool, error)
	RemoveImage(ctx context.Context, reference string) ([]engine.ImageDelete, error)
	Inspect(ctx context.Context, name string) (engine.ContainerInfo, error)
}

// Config controls node-local disk retention for managed artifacts.
type Config struct {
	StatePath                string
	RetainedPreviousReleases int
	ProtectedImages          []string
	ContainerLogMaxSize      string
	ContainerLogMaxFile      int
}

// Manager runs automatic node-local cleanup for devopsellence-managed artifacts.
type Manager struct {
	engine Engine
	cfg    Config
	logger *slog.Logger
}

// New creates a disk-care manager.
func New(engine Engine, cfg Config, logger *slog.Logger) *Manager {
	if cfg.RetainedPreviousReleases < 0 {
		cfg.RetainedPreviousReleases = defaultRetainedPreviousReleases
	}
	return &Manager{engine: engine, cfg: cfg, logger: logger}
}

// Run records the current desired-state images, removes old managed image
// references outside the retention window, and reports local log usage.
func (m *Manager) Run(ctx context.Context, desired *desiredstatepb.DesiredState) (*report.DiskCareStatus, error) {
	status := &report.DiskCareStatus{LastCleanupAt: time.Now().UTC()}
	if m == nil {
		return status, nil
	}
	status.RetainedPreviousReleases = m.cfg.RetainedPreviousReleases
	status.LogMaxSize = m.cfg.ContainerLogMaxSize
	status.LogMaxFile = m.cfg.ContainerLogMaxFile
	if m.engine == nil || desired == nil {
		return status, nil
	}

	store, loadWarning, err := m.loadStore()
	if loadWarning != "" {
		status.LastError = loadWarning
	}
	if err != nil {
		status.LastError = err.Error()
		return status, err
	}

	now := status.LastCleanupAt
	current := releasesFromDesired(desired, now)
	store.upsert(current)

	managed, err := m.engine.ListManaged(ctx)
	if err != nil {
		status.LastError = fmt.Sprintf("list managed containers: %v", err)
		return status, err
	}
	allContainers, err := m.engine.ListContainers(ctx)
	if err != nil {
		status.LastError = fmt.Sprintf("list containers: %v", err)
		return status, err
	}
	images, err := m.engine.ListImages(ctx)
	if err != nil {
		status.LastError = fmt.Sprintf("list images: %v", err)
		return status, err
	}

	imageSizes := imageSizeIndex(images)
	imageRefs := imageRefSet(images)
	protectedImages := stringSet(m.cfg.ProtectedImages)
	usedImages := usedContainerImages(allContainers)

	retainedKeys := retainedReleaseKeys(store.Releases, current, m.cfg.RetainedPreviousReleases+1)
	status.RetainedReleaseCount = len(retainedKeys)
	retainedImages := retainedImageRefs(store.Releases, retainedKeys, current, protectedImages, usedImages)

	removedImages := map[string]struct{}{}
	var firstErr error
	for _, image := range pruneCandidates(store.Releases, retainedKeys, retainedImages, protectedImages) {
		if _, ok := usedImages[image]; ok {
			continue
		}
		exists, existsErr := m.imageExists(ctx, image, imageRefs)
		if existsErr != nil {
			firstErr = joinFirst(firstErr, fmt.Errorf("inspect image %s: %w", image, existsErr))
			continue
		}
		if !exists {
			removedImages[image] = struct{}{}
			continue
		}

		size := sizeForImage(image, imageSizes)
		removeResp, removeErr := m.engine.RemoveImage(ctx, image)
		if removeErr != nil {
			firstErr = joinFirst(firstErr, fmt.Errorf("remove image %s: %w", image, removeErr))
			continue
		}
		reclaimedBytes := reclaimedBytesForRemoval(removeResp, size)
		removedImages[image] = struct{}{}
		status.ReclaimedBytes += reclaimedBytes
		status.RemovedArtifacts = append(status.RemovedArtifacts, report.DiskCareArtifact{
			Type:      "image",
			Reference: image,
			Reason:    "older_than_retention_window",
			Bytes:     reclaimedBytes,
		})
	}

	store.dropRemovedImages(removedImages, retainedKeys)
	if saveErr := m.saveStore(store); saveErr != nil {
		firstErr = joinFirst(firstErr, saveErr)
	}

	logBytes, logErr := m.dockerLogBytes(ctx, managed)
	if logErr != nil {
		firstErr = joinFirst(firstErr, logErr)
	}
	status.DockerLogBytes = logBytes

	if firstErr != nil {
		status.LastError = firstErr.Error()
		if m.logger != nil {
			m.logger.Warn("disk care completed with errors", "error", firstErr)
		}
		return status, firstErr
	}
	return status, nil
}

func (m *Manager) imageExists(ctx context.Context, image string, imageRefs map[string]struct{}) (bool, error) {
	if _, ok := imageRefs[image]; ok {
		return true, nil
	}
	return m.engine.ImageExists(ctx, image)
}

func reclaimedBytesForRemoval(removed []engine.ImageDelete, size int64) int64 {
	for _, item := range removed {
		if strings.TrimSpace(item.Deleted) != "" {
			return size
		}
	}
	return 0
}

func logPaths(path string, maxFile int) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	if maxFile < 1 {
		maxFile = 1
	}
	paths := make([]string, 0, maxFile)
	paths = append(paths, path)
	for i := 1; i < maxFile; i++ {
		paths = append(paths, fmt.Sprintf("%s.%d", path, i))
	}
	return paths
}

func (m *Manager) dockerLogBytes(ctx context.Context, containers []engine.ContainerState) (int64, error) {
	var total int64
	var firstErr error
	for _, container := range containers {
		if strings.TrimSpace(container.Name) == "" {
			continue
		}
		info, err := m.engine.Inspect(ctx, container.Name)
		if err != nil {
			firstErr = joinFirst(firstErr, fmt.Errorf("inspect container %s for logs: %w", container.Name, err))
			continue
		}
		if strings.TrimSpace(info.LogPath) == "" {
			continue
		}
		for _, path := range logPaths(info.LogPath, m.cfg.ContainerLogMaxFile) {
			stat, err := os.Stat(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				firstErr = joinFirst(firstErr, fmt.Errorf("stat log %s: %w", path, err))
				continue
			}
			total += stat.Size()
		}
	}
	return total, firstErr
}

type store struct {
	Releases []releaseRecord `json:"releases,omitempty"`
}

type releaseRecord struct {
	Environment string    `json:"environment"`
	Revision    string    `json:"revision"`
	Images      []string  `json:"images,omitempty"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

func (m *Manager) loadStore() (*store, string, error) {
	if strings.TrimSpace(m.cfg.StatePath) == "" {
		return &store{}, "", nil
	}
	data, err := os.ReadFile(m.cfg.StatePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &store{}, "", nil
		}
		return nil, "", fmt.Errorf("read disk care state: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return &store{}, "", nil
	}
	var s store
	if err := json.Unmarshal(data, &s); err != nil {
		warning := fmt.Sprintf("ignored corrupt disk care state: %v", err)
		if m.logger != nil {
			m.logger.Warn("ignoring corrupt disk care state", "path", m.cfg.StatePath, "error", err)
		}
		return &store{}, warning, nil
	}
	return &s, "", nil
}

func (m *Manager) saveStore(s *store) error {
	if strings.TrimSpace(m.cfg.StatePath) == "" {
		return nil
	}
	dir := filepath.Dir(m.cfg.StatePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create disk care state dir: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("set disk care state dir permissions: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal disk care state: %w", err)
	}
	data = append(data, '\n')
	if err := writeFileAtomic(m.cfg.StatePath, data, 0o600); err != nil {
		return fmt.Errorf("write disk care state: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func releasesFromDesired(state *desiredstatepb.DesiredState, now time.Time) []releaseRecord {
	releases := map[string]*releaseRecord{}
	for _, runtime := range desiredstate.RuntimeServices(state) {
		addReleaseImage(releases, runtime.EnvironmentName, runtime.EnvironmentRevision, runtime.Service.GetImage(), now)
	}
	for _, runtime := range desiredstate.RuntimeTasks(state) {
		addReleaseImage(releases, runtime.EnvironmentName, runtime.EnvironmentRevision, runtime.Task.GetImage(), now)
	}
	out := make([]releaseRecord, 0, len(releases))
	for _, release := range releases {
		sort.Strings(release.Images)
		out = append(out, *release)
	}
	return out
}

func addReleaseImage(releases map[string]*releaseRecord, environment string, revision string, image string, now time.Time) {
	environment = strings.TrimSpace(environment)
	revision = strings.TrimSpace(revision)
	image = strings.TrimSpace(image)
	if environment == "" || revision == "" || image == "" {
		return
	}
	key := releaseKey(environment, revision)
	release := releases[key]
	if release == nil {
		release = &releaseRecord{Environment: environment, Revision: revision, LastSeenAt: now}
		releases[key] = release
	}
	if !containsString(release.Images, image) {
		release.Images = append(release.Images, image)
	}
}

func (s *store) upsert(releases []releaseRecord) {
	byKey := map[string]int{}
	for i, release := range s.Releases {
		byKey[releaseKey(release.Environment, release.Revision)] = i
	}
	for _, release := range releases {
		key := releaseKey(release.Environment, release.Revision)
		if idx, ok := byKey[key]; ok {
			existing := &s.Releases[idx]
			existing.Images = mergeStrings(existing.Images, release.Images)
			existing.LastSeenAt = release.LastSeenAt
			continue
		}
		s.Releases = append(s.Releases, release)
	}
}

func (s *store) dropRemovedImages(removed map[string]struct{}, retainedKeys map[string]struct{}) {
	if len(removed) == 0 {
		return
	}
	kept := s.Releases[:0]
	for _, release := range s.Releases {
		key := releaseKey(release.Environment, release.Revision)
		if _, retained := retainedKeys[key]; retained {
			kept = append(kept, release)
			continue
		}
		images := release.Images[:0]
		for _, image := range release.Images {
			if _, ok := removed[image]; !ok {
				images = append(images, image)
			}
		}
		release.Images = images
		if len(release.Images) > 0 {
			kept = append(kept, release)
		}
	}
	s.Releases = kept
}

func retainedReleaseKeys(releases []releaseRecord, current []releaseRecord, keepPerEnvironment int) map[string]struct{} {
	if keepPerEnvironment < 1 {
		keepPerEnvironment = 1
	}
	retained := map[string]struct{}{}
	for _, release := range current {
		retained[releaseKey(release.Environment, release.Revision)] = struct{}{}
	}
	byEnvironment := map[string][]releaseRecord{}
	for _, release := range releases {
		byEnvironment[release.Environment] = append(byEnvironment[release.Environment], release)
	}
	for _, envReleases := range byEnvironment {
		sort.SliceStable(envReleases, func(i, j int) bool {
			return envReleases[i].LastSeenAt.After(envReleases[j].LastSeenAt)
		})
		limit := keepPerEnvironment
		if len(envReleases) < limit {
			limit = len(envReleases)
		}
		for i := 0; i < limit; i++ {
			retained[releaseKey(envReleases[i].Environment, envReleases[i].Revision)] = struct{}{}
		}
	}
	return retained
}

func retainedImageRefs(releases []releaseRecord, retainedKeys map[string]struct{}, current []releaseRecord, protectedImages map[string]struct{}, usedImages map[string]struct{}) map[string]struct{} {
	retained := map[string]struct{}{}
	for image := range protectedImages {
		retained[image] = struct{}{}
	}
	for image := range usedImages {
		retained[image] = struct{}{}
	}
	for _, release := range current {
		for _, image := range release.Images {
			retained[image] = struct{}{}
		}
	}
	for _, release := range releases {
		if _, ok := retainedKeys[releaseKey(release.Environment, release.Revision)]; !ok {
			continue
		}
		for _, image := range release.Images {
			retained[image] = struct{}{}
		}
	}
	return retained
}

func pruneCandidates(releases []releaseRecord, retainedKeys map[string]struct{}, retainedImages map[string]struct{}, protectedImages map[string]struct{}) []string {
	candidates := map[string]struct{}{}
	for _, release := range releases {
		if _, ok := retainedKeys[releaseKey(release.Environment, release.Revision)]; ok {
			continue
		}
		for _, image := range release.Images {
			if _, ok := retainedImages[image]; ok {
				continue
			}
			if _, ok := protectedImages[image]; ok {
				continue
			}
			candidates[image] = struct{}{}
		}
	}
	out := make([]string, 0, len(candidates))
	for image := range candidates {
		out = append(out, image)
	}
	sort.Strings(out)
	return out
}

func imageRefSet(images []engine.ImageState) map[string]struct{} {
	refs := map[string]struct{}{}
	for _, image := range images {
		for _, ref := range imageRefs(image) {
			refs[ref] = struct{}{}
		}
	}
	return refs
}

func imageSizeIndex(images []engine.ImageState) map[string]int64 {
	index := map[string]int64{}
	for _, image := range images {
		for _, ref := range imageRefs(image) {
			index[ref] = image.Size
		}
	}
	return index
}

func imageRefs(image engine.ImageState) []string {
	refs := make([]string, 0, len(image.RepoTags)+len(image.RepoDigests)+1)
	refs = append(refs, image.ID)
	refs = append(refs, image.RepoTags...)
	refs = append(refs, image.RepoDigests...)
	return refs
}

func sizeForImage(image string, sizes map[string]int64) int64 {
	if size, ok := sizes[image]; ok {
		return size
	}
	return 0
}

func usedContainerImages(containers []engine.ContainerState) map[string]struct{} {
	used := map[string]struct{}{}
	for _, container := range containers {
		image := strings.TrimSpace(container.Image)
		if image == "" {
			continue
		}
		used[image] = struct{}{}
	}
	return used
}

func stringSet(values []string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	return set
}

func mergeStrings(a []string, b []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(a)+len(b))
	for _, value := range append(a, b...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func releaseKey(environment string, revision string) string {
	return strings.TrimSpace(environment) + "\x00" + strings.TrimSpace(revision)
}

func joinFirst(first error, next error) error {
	if first != nil {
		return first
	}
	return next
}
