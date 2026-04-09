package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type state struct {
	mu              sync.Mutex
	events          []map[string]any
	buckets         map[string]*bucket
	repositories    map[string]*repository
	secrets         map[string]*secret
	serviceAccounts map[string]*serviceAccount
	kmsKeys         map[string]*rsa.PrivateKey
	releaseFiles    map[string]*releaseFile
}

type bucket struct {
	Name     string                   `json:"name"`
	Project  string                   `json:"project"`
	Bindings []map[string]any         `json:"bindings"`
	Objects  map[string]*bucketObject `json:"objects"`
}

type bucketObject struct {
	Name       string `json:"name"`
	Generation int64  `json:"generation"`
	ETag       string `json:"etag"`
	Body       []byte `json:"-"`
}

type repository struct {
	Name     string           `json:"name"`
	Project  string           `json:"project"`
	Location string           `json:"location"`
	ID       string           `json:"id"`
	Bindings []map[string]any `json:"bindings"`
}

type secret struct {
	Name     string           `json:"name"`
	Project  string           `json:"project"`
	ID       string           `json:"id"`
	Bindings []map[string]any `json:"bindings"`
	Versions [][]byte         `json:"-"`
}

type serviceAccount struct {
	Name        string           `json:"name"`
	Project     string           `json:"project"`
	Email       string           `json:"email"`
	DisplayName string           `json:"display_name"`
	Bindings    []map[string]any `json:"bindings"`
}

type releaseFile struct {
	Project   string `json:"project"`
	Location  string `json:"location"`
	Repo      string `json:"repository"`
	Package   string `json:"package"`
	Version   string `json:"version"`
	Source    string `json:"source_name"`
	SizeBytes int    `json:"size_bytes"`
	Body      []byte `json:"-"`
}

type seedReleaseRequest struct {
	Entries []struct {
		ProjectID  string `json:"project_id"`
		Location   string `json:"location"`
		Repository string `json:"repository"`
		Package    string `json:"package"`
		Version    string `json:"version"`
		SourceName string `json:"source_name"`
		ContentB64 string `json:"content_b64"`
	} `json:"entries"`
}

type eventAssertionRequest struct {
	EventType      string            `json:"event_type"`
	Match          map[string]string `json:"match"`
	MinCount       int               `json:"min_count"`
	TimeoutSeconds int               `json:"timeout_seconds"`
}

func main() {
	var listenAddr string
	flag.StringVar(&listenAddr, "listen", "127.0.0.1:4601", "listen address")
	flag.Parse()

	s := &state{}
	s.resetLocked()

	mux := http.NewServeMux()
	mux.HandleFunc("/__admin/reset", s.handleAdminReset)
	mux.HandleFunc("/__admin/seed/releases", s.handleAdminSeedReleases)
	mux.HandleFunc("/__admin/state", s.handleAdminState)
	mux.HandleFunc("/__admin/events", s.handleAdminEvents)
	mux.HandleFunc("/__admin/wait", s.handleAdminWait)
	mux.HandleFunc("/__admin/assert", s.handleAdminAssert)
	mux.HandleFunc("/storage/v1/b", s.handleBuckets)
	mux.HandleFunc("/storage/v1/b/", s.handleStorage)
	mux.HandleFunc("/upload/storage/v1/b/", s.handleStorageUpload)
	mux.HandleFunc("/secretmanager/v1/projects/", s.handleSecretManager)
	mux.HandleFunc("/artifactregistry/v1/projects/", s.handleArtifactRegistry)
	mux.HandleFunc("/artifactregistry/download/v1/projects/", s.handleArtifactRegistryDownload)
	mux.HandleFunc("/iam/v1/projects/", s.handleIAMProjects)
	mux.HandleFunc("/iam/v1/projects/-/serviceAccounts/", s.handleIAMProjects)
	mux.HandleFunc("/iamcredentials/v1/projects/-/serviceAccounts/", s.handleIAMCredentials)
	mux.HandleFunc("/sts/v1/token", s.handleSTS)
	mux.HandleFunc("/cloudkms/v1/", s.handleKMS)

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("gcp-mock listening on http://%s", listenAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func (s *state) handleAdminReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *state) handleAdminSeedReleases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req seedReleaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range req.Entries {
		body, err := base64.StdEncoding.DecodeString(entry.ContentB64)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		key := releaseKey(entry.ProjectID, entry.Location, entry.Repository, entry.Package, entry.Version, entry.SourceName)
		s.releaseFiles[key] = &releaseFile{
			Project:   entry.ProjectID,
			Location:  entry.Location,
			Repo:      entry.Repository,
			Package:   entry.Package,
			Version:   entry.Version,
			Source:    entry.SourceName,
			SizeBytes: len(body),
			Body:      body,
		}
		s.recordLocked("release_seeded", map[string]any{
			"project":     entry.ProjectID,
			"location":    entry.Location,
			"repository":  entry.Repository,
			"package":     entry.Package,
			"version":     entry.Version,
			"source_name": entry.SourceName,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"seeded": len(req.Entries)})
}

func (s *state) handleAdminState(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"buckets":          s.buckets,
		"repositories":     s.repositories,
		"secrets":          s.secrets,
		"service_accounts": s.serviceAccounts,
		"release_files":    s.releaseFiles,
	})
}

func (s *state) handleAdminEvents(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"events": s.events})
}

func (s *state) handleAdminWait(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	req, err := decodeEventAssertionRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	deadline := time.Now().Add(time.Duration(req.TimeoutSeconds) * time.Second)
	for {
		s.mu.Lock()
		count := s.countMatchingEventsLocked(req)
		s.mu.Unlock()
		if count >= req.MinCount {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "matched": count})
			return
		}
		if time.Now().After(deadline) {
			http.Error(w, fmt.Sprintf("timed out waiting for %s (%d < %d)", req.EventType, count, req.MinCount), http.StatusRequestTimeout)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *state) handleAdminAssert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	req, err := decodeEventAssertionRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	count := s.countMatchingEventsLocked(req)
	if count < req.MinCount {
		http.Error(w, fmt.Sprintf("expected %d matching %s event(s), got %d", req.MinCount, req.EventType, count), http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "matched": count})
}

func (s *state) handleBuckets(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		project := r.URL.Query().Get("project")
		items := []map[string]any{}
		for _, bucket := range s.buckets {
			if project == "" || bucket.Project == project {
				items = append(items, map[string]any{"name": bucket.Name})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		project := r.URL.Query().Get("project")
		var payload struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload.Name == "" {
			http.Error(w, "missing bucket name", http.StatusBadRequest)
			return
		}
		if _, ok := s.buckets[payload.Name]; ok {
			http.Error(w, "already exists", http.StatusConflict)
			return
		}
		s.buckets[payload.Name] = &bucket{
			Name:     payload.Name,
			Project:  project,
			Bindings: []map[string]any{},
			Objects:  map[string]*bucketObject{},
		}
		s.recordLocked("bucket_created", map[string]any{"bucket": payload.Name, "project": project})
		writeJSON(w, http.StatusOK, map[string]any{"name": payload.Name})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *state) handleStorageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/upload/storage/v1/b/"), "/")
	if len(parts) < 2 || parts[1] != "o" {
		http.NotFound(w, r)
		return
	}
	bucketName, _ := url.PathUnescape(parts[0])
	objectName, _ := url.QueryUnescape(r.URL.Query().Get("name"))
	if objectName == "" {
		http.Error(w, "missing object name", http.StatusBadRequest)
		return
	}
	body, _ := ioReadAll(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	bkt := s.ensureBucketLocked(bucketName, "")
	current, ok := bkt.Objects[objectName]
	generation := int64(1)
	if ok {
		generation = current.Generation + 1
	}
	etag := fmt.Sprintf("etag-%d", generation)
	bkt.Objects[objectName] = &bucketObject{Name: objectName, Generation: generation, ETag: etag, Body: body}
	s.recordLocked("object_written", map[string]any{"bucket": bucketName, "object": objectName, "generation": generation})
	writeJSON(w, http.StatusOK, map[string]any{"name": objectName, "generation": strconv.FormatInt(generation, 10), "etag": etag})
}

func (s *state) handleStorage(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/storage/v1/b/")
	parts := strings.Split(trimmed, "/")
	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}
	bucketName, _ := url.PathUnescape(parts[0])
	s.mu.Lock()
	defer s.mu.Unlock()
	bkt := s.ensureBucketLocked(bucketName, "")
	if len(parts) == 2 && parts[1] == "iam" {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, map[string]any{"bindings": bkt.Bindings})
		case http.MethodPut:
			var payload struct {
				Bindings []map[string]any `json:"bindings"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			bkt.Bindings = payload.Bindings
			s.recordLocked("bucket_policy_updated", map[string]any{"bucket": bucketName})
			writeJSON(w, http.StatusOK, map[string]any{"bindings": bkt.Bindings})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) >= 2 && parts[1] == "o" {
		if len(parts) == 2 {
			items := []map[string]any{}
			prefix := r.URL.Query().Get("prefix")
			for _, object := range bkt.Objects {
				if prefix == "" || strings.HasPrefix(object.Name, prefix) {
					items = append(items, map[string]any{"name": object.Name})
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
			return
		}
		objectPath, _ := url.PathUnescape(strings.Join(parts[2:], "/"))
		object := bkt.Objects[objectPath]
		if object == nil {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("alt") == "media" {
			s.recordLocked("object_read", map[string]any{"bucket": bucketName, "object": objectPath})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(object.Body)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"generation": strconv.FormatInt(object.Generation, 10),
			"etag":       object.ETag,
		})
		return
	}
	if r.Method == http.MethodDelete {
		delete(s.buckets, bucketName)
		s.recordLocked("bucket_deleted", map[string]any{"bucket": bucketName})
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
		return
	}
	http.NotFound(w, r)
}

func (s *state) handleSecretManager(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/secretmanager/v1/projects/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	project := parts[0]
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(parts) == 2 && parts[1] == "secrets" {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		secretID := r.URL.Query().Get("secretId")
		if secretID == "" {
			http.Error(w, "missing secretId", http.StatusBadRequest)
			return
		}
		key := secretKey(project, secretID)
		if _, ok := s.secrets[key]; ok {
			http.Error(w, "already exists", http.StatusConflict)
			return
		}
		s.secrets[key] = &secret{Name: fmt.Sprintf("projects/%s/secrets/%s", project, secretID), Project: project, ID: secretID, Bindings: []map[string]any{}, Versions: [][]byte{}}
		s.recordLocked("secret_created", map[string]any{"project": project, "secret": secretID})
		writeJSON(w, http.StatusOK, map[string]any{"name": s.secrets[key].Name})
		return
	}
	if len(parts) < 3 || parts[1] != "secrets" {
		http.NotFound(w, r)
		return
	}
	secretID := strings.SplitN(parts[2], ":", 2)[0]
	sec := s.ensureSecretLocked(project, secretID)
	switch {
	case strings.HasSuffix(r.URL.Path, ":addVersion"):
		var payload struct {
			Payload struct {
				Data string `json:"data"`
			} `json:"payload"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		body, err := base64.StdEncoding.DecodeString(payload.Payload.Data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sec.Versions = append(sec.Versions, body)
		version := len(sec.Versions)
		s.recordLocked("secret_version_added", map[string]any{"project": project, "secret": secretID, "version": version})
		writeJSON(w, http.StatusOK, map[string]any{"name": fmt.Sprintf("%s/versions/%d", sec.Name, version)})
	case strings.HasSuffix(r.URL.Path, ":getIamPolicy"):
		writeJSON(w, http.StatusOK, map[string]any{"bindings": sec.Bindings})
	case strings.HasSuffix(r.URL.Path, ":setIamPolicy"):
		var payload struct {
			Policy struct {
				Bindings []map[string]any `json:"bindings"`
			} `json:"policy"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		sec.Bindings = payload.Policy.Bindings
		s.recordLocked("secret_policy_updated", map[string]any{"project": project, "secret": secretID})
		writeJSON(w, http.StatusOK, map[string]any{"bindings": sec.Bindings})
	case strings.Contains(r.URL.Path, "/versions/") && strings.HasSuffix(r.URL.Path, ":access"):
		if len(sec.Versions) == 0 {
			http.NotFound(w, r)
			return
		}
		versionBody := sec.Versions[len(sec.Versions)-1]
		s.recordLocked("secret_accessed", map[string]any{"project": project, "secret": secretID})
		writeJSON(w, http.StatusOK, map[string]any{"payload": map[string]any{"data": base64.StdEncoding.EncodeToString(versionBody)}})
	case r.Method == http.MethodDelete:
		delete(s.secrets, secretKey(project, secretID))
		s.recordLocked("secret_deleted", map[string]any{"project": project, "secret": secretID})
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
	default:
		http.NotFound(w, r)
	}
}

func (s *state) handleArtifactRegistry(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/artifactregistry/v1/projects/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}
	project, location := parts[0], parts[2]
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(parts) == 4 && parts[3] == "repositories" {
		switch r.Method {
		case http.MethodGet:
			repos := []map[string]any{}
			for _, repo := range s.repositories {
				if repo.Project == project && repo.Location == location {
					repos = append(repos, map[string]any{"name": repo.Name})
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{"repositories": repos})
		case http.MethodPost:
			repositoryID := r.URL.Query().Get("repositoryId")
			if repositoryID == "" {
				http.Error(w, "missing repositoryId", http.StatusBadRequest)
				return
			}
			key := repositoryKey(project, location, repositoryID)
			if _, ok := s.repositories[key]; ok {
				http.Error(w, "already exists", http.StatusConflict)
				return
			}
			name := fmt.Sprintf("projects/%s/locations/%s/repositories/%s", project, location, repositoryID)
			s.repositories[key] = &repository{Name: name, Project: project, Location: location, ID: repositoryID, Bindings: []map[string]any{}}
			s.recordLocked("repository_created", map[string]any{"project": project, "location": location, "repository": repositoryID})
			writeJSON(w, http.StatusOK, map[string]any{"name": name})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 5 && parts[3] == "repositories" {
		repositoryID := parts[4]
		repo := s.ensureRepositoryLocked(project, location, repositoryID)
		writeJSON(w, http.StatusOK, map[string]any{"name": repo.Name})
		return
	}
	if len(parts) == 6 && parts[3] == "repositories" && parts[5] == "files" {
		repositoryID := parts[4]
		query := r.URL.Query().Get("filter")
		owner := strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(query, `owner="`), `"`), "")
		ownerParts := strings.Split(owner, "/packages/")
		version := ""
		pkg := ""
		if len(ownerParts) == 2 {
			tail := strings.Split(ownerParts[1], "/versions/")
			if len(tail) == 2 {
				pkg = tail[0]
				version = tail[1]
			}
		}
		files := []map[string]any{}
		prefix := fmt.Sprintf("projects/%s/locations/%s/repositories/%s", project, location, repositoryID)
		for _, file := range s.releaseFiles {
			if file.Project == project && file.Location == location && file.Repo == repositoryID && file.Package == pkg && file.Version == version {
				files = append(files, map[string]any{
					"name": fmt.Sprintf("%s/files/%s", prefix, file.Source),
				})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"files": files})
		return
	}
	if len(parts) == 5 && parts[3] == "repositories" && strings.HasSuffix(parts[4], ":getIamPolicy") {
		repositoryID := strings.TrimSuffix(parts[4], ":getIamPolicy")
		repo := s.ensureRepositoryLocked(project, location, repositoryID)
		writeJSON(w, http.StatusOK, map[string]any{"bindings": repo.Bindings})
		return
	}
	if len(parts) == 5 && parts[3] == "repositories" && strings.HasSuffix(parts[4], ":setIamPolicy") {
		repositoryID := strings.TrimSuffix(parts[4], ":setIamPolicy")
		repo := s.ensureRepositoryLocked(project, location, repositoryID)
		var payload struct {
			Policy struct {
				Bindings []map[string]any `json:"bindings"`
			} `json:"policy"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		repo.Bindings = payload.Policy.Bindings
		s.recordLocked("repository_policy_updated", map[string]any{"project": project, "location": location, "repository": repositoryID})
		writeJSON(w, http.StatusOK, map[string]any{"bindings": repo.Bindings})
		return
	}
	http.NotFound(w, r)
}

func (s *state) handleArtifactRegistryDownload(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/artifactregistry/download/v1/projects/")
	parts := strings.Split(trimmed, "/")
	if len(parts) < 7 || parts[1] != "locations" || parts[3] != "repositories" || parts[5] != "files" {
		http.NotFound(w, r)
		return
	}
	project, location, repositoryID := parts[0], parts[2], parts[4]
	namePart := strings.SplitN(parts[6], ":download", 2)[0]
	sourceName, _ := url.PathUnescape(namePart)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, file := range s.releaseFiles {
		if file.Project == project && file.Location == location && file.Repo == repositoryID && file.Source == sourceName {
			s.recordLocked("release_downloaded", map[string]any{"project": project, "location": location, "repository": repositoryID, "source_name": sourceName})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(file.Body)
			return
		}
	}
	http.NotFound(w, r)
}

func (s *state) handleIAMProjects(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/iam/v1/projects/")
	parts := strings.Split(trimmed, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(parts) == 2 && parts[1] == "serviceAccounts" && r.Method == http.MethodPost {
		project := parts[0]
		var payload struct {
			AccountID      string `json:"accountId"`
			ServiceAccount struct {
				DisplayName string `json:"displayName"`
			} `json:"serviceAccount"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		email := payload.AccountID + "@" + project + ".iam.gserviceaccount.com"
		key := serviceAccountKey(project, email)
		s.serviceAccounts[key] = &serviceAccount{
			Name:        fmt.Sprintf("projects/%s/serviceAccounts/%s", project, email),
			Project:     project,
			Email:       email,
			DisplayName: payload.ServiceAccount.DisplayName,
			Bindings:    []map[string]any{},
		}
		s.recordLocked("service_account_created", map[string]any{"project": project, "email": email})
		writeJSON(w, http.StatusOK, map[string]any{"name": s.serviceAccounts[key].Name, "email": email})
		return
	}
	if len(parts) >= 3 && parts[1] == "serviceAccounts" {
		project := parts[0]
		emailPart := strings.Join(parts[2:], "/")
		switch {
		case strings.HasSuffix(emailPart, ":getIamPolicy"):
			email := strings.TrimSuffix(emailPart, ":getIamPolicy")
			account := s.ensureServiceAccountLocked(normalizeIAMProject(project), email)
			writeJSON(w, http.StatusOK, map[string]any{"bindings": account.Bindings})
		case strings.HasSuffix(emailPart, ":setIamPolicy"):
			email := strings.TrimSuffix(emailPart, ":setIamPolicy")
			account := s.ensureServiceAccountLocked(normalizeIAMProject(project), email)
			var payload struct {
				Policy struct {
					Bindings []map[string]any `json:"bindings"`
				} `json:"policy"`
			}
			_ = json.NewDecoder(r.Body).Decode(&payload)
			account.Bindings = payload.Policy.Bindings
			s.recordLocked("service_account_policy_updated", map[string]any{"project": normalizeIAMProject(project), "email": email})
			writeJSON(w, http.StatusOK, map[string]any{"bindings": account.Bindings})
		case r.Method == http.MethodGet:
			email, _ := url.PathUnescape(emailPart)
			account := s.serviceAccounts[serviceAccountKey(normalizeIAMProject(project), email)]
			if account == nil {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"name": account.Name, "email": account.Email, "displayName": account.DisplayName})
		default:
			http.NotFound(w, r)
		}
		return
	}
	http.NotFound(w, r)
}

func (s *state) handleIAMCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	trimmed := strings.TrimPrefix(r.URL.Path, "/iamcredentials/v1/projects/-/serviceAccounts/")
	email := strings.TrimSuffix(trimmed, ":generateAccessToken")
	email, _ = url.PathUnescape(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordLocked("access_token_generated", map[string]any{"email": email})
	writeJSON(w, http.StatusOK, map[string]any{
		"accessToken": fmt.Sprintf("fake-access-%s", strings.ReplaceAll(email, "@", "_")),
		"expireTime":  time.Now().Add(20 * time.Minute).UTC().Format(time.RFC3339),
	})
}

func (s *state) handleSTS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload map[string]any
	_ = json.NewDecoder(r.Body).Decode(&payload)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recordLocked("sts_exchanged", map[string]any{"audience": payload["audience"]})
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": "fake-federated-access-token",
		"expires_in":   1200,
		"token_type":   "Bearer",
	})
}

func (s *state) handleKMS(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/cloudkms/v1/")
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case strings.HasSuffix(trimmed, "/publicKey"):
		keyVersion := strings.TrimSuffix(trimmed, "/publicKey")
		privateKey := s.ensureKMSKeyLocked(keyVersion)
		der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		pemBody := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
		writeJSON(w, http.StatusOK, map[string]any{"pem": string(pemBody)})
	case strings.HasSuffix(trimmed, ":asymmetricSign"):
		keyVersion := strings.TrimSuffix(trimmed, ":asymmetricSign")
		privateKey := s.ensureKMSKeyLocked(keyVersion)
		var payload struct {
			Digest struct {
				SHA256 string `json:"sha256"`
			} `json:"digest"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		digest, err := base64.StdEncoding.DecodeString(payload.Digest.SHA256)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.recordLocked("kms_signed", map[string]any{"key_version": keyVersion})
		writeJSON(w, http.StatusOK, map[string]any{"signature": base64.StdEncoding.EncodeToString(signature)})
	default:
		http.NotFound(w, r)
	}
}

func (s *state) resetLocked() {
	s.events = []map[string]any{}
	s.buckets = map[string]*bucket{}
	s.repositories = map[string]*repository{}
	s.secrets = map[string]*secret{}
	s.serviceAccounts = map[string]*serviceAccount{}
	s.kmsKeys = map[string]*rsa.PrivateKey{}
	s.releaseFiles = map[string]*releaseFile{}
}

func (s *state) ensureBucketLocked(name, project string) *bucket {
	bkt := s.buckets[name]
	if bkt == nil {
		bkt = &bucket{Name: name, Project: project, Bindings: []map[string]any{}, Objects: map[string]*bucketObject{}}
		s.buckets[name] = bkt
	}
	return bkt
}

func (s *state) ensureRepositoryLocked(project, location, repositoryID string) *repository {
	key := repositoryKey(project, location, repositoryID)
	repo := s.repositories[key]
	if repo == nil {
		repo = &repository{Name: fmt.Sprintf("projects/%s/locations/%s/repositories/%s", project, location, repositoryID), Project: project, Location: location, ID: repositoryID, Bindings: []map[string]any{}}
		s.repositories[key] = repo
	}
	return repo
}

func (s *state) ensureSecretLocked(project, secretID string) *secret {
	key := secretKey(project, secretID)
	sec := s.secrets[key]
	if sec == nil {
		sec = &secret{Name: fmt.Sprintf("projects/%s/secrets/%s", project, secretID), Project: project, ID: secretID, Bindings: []map[string]any{}, Versions: [][]byte{}}
		s.secrets[key] = sec
	}
	return sec
}

func (s *state) ensureServiceAccountLocked(project, email string) *serviceAccount {
	key := serviceAccountKey(project, email)
	account := s.serviceAccounts[key]
	if account == nil {
		account = &serviceAccount{Name: fmt.Sprintf("projects/%s/serviceAccounts/%s", project, email), Project: project, Email: email, Bindings: []map[string]any{}}
		s.serviceAccounts[key] = account
	}
	return account
}

func (s *state) ensureKMSKeyLocked(keyVersion string) *rsa.PrivateKey {
	privateKey := s.kmsKeys[keyVersion]
	if privateKey == nil {
		var err error
		privateKey, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		s.kmsKeys[keyVersion] = privateKey
	}
	return privateKey
}

func (s *state) recordLocked(eventType string, attrs map[string]any) {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", eventType, len(s.events))))
	attrs["id"] = fmt.Sprintf("%x", sum[:8])
	attrs["type"] = eventType
	attrs["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	s.events = append(s.events, attrs)
}

func (s *state) countMatchingEventsLocked(req eventAssertionRequest) int {
	count := 0
	for _, event := range s.events {
		if req.EventType != "" && fmt.Sprint(event["type"]) != req.EventType {
			continue
		}
		if !eventMatches(event, req.Match) {
			continue
		}
		count++
	}
	return count
}

func repositoryKey(project, location, repository string) string {
	return path.Join(project, location, repository)
}

func releaseKey(project, location, repository, pkg, version, source string) string {
	return path.Join(project, location, repository, pkg, version, source)
}

func secretKey(project, secret string) string {
	return path.Join(project, secret)
}

func serviceAccountKey(project, email string) string {
	return project + ":" + email
}

func normalizeIAMProject(project string) string {
	if project == "-" {
		return "-"
	}
	return project
}

func decodeEventAssertionRequest(r *http.Request) (eventAssertionRequest, error) {
	defer r.Body.Close()
	var req eventAssertionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return eventAssertionRequest{}, err
	}
	if strings.TrimSpace(req.EventType) == "" {
		return eventAssertionRequest{}, errors.New("event_type is required")
	}
	if req.MinCount <= 0 {
		req.MinCount = 1
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = 30
	}
	if req.Match == nil {
		req.Match = map[string]string{}
	}
	return req, nil
}

func eventMatches(event map[string]any, match map[string]string) bool {
	for key, want := range match {
		if fmt.Sprint(event[key]) != want {
			return false
		}
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func ioReadAll(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}
