package remote

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/auth"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

type fakeRemoteServer struct {
	mu sync.Mutex

	currentAccessToken  string
	currentRefreshToken string

	bootstrapCalls     int
	assignmentCalls    int
	subjectCalls       int
	googleSTSCalls     int
	impersonationCalls int
	gcsMetadataCalls   int
	gcsMediaCalls      int
	httpDesiredCalls   int
	secretCalls        int
	httpSecretCalls    int

	desiredStateKey   *rsa.PrivateKey
	nodeIdentityKey   *rsa.PrivateKey
	desiredStateURI   string
	assignmentMode    string
	desiredSequence   int64
	unassignedPayload []byte
	desiredGeneration string
	desiredETag       string
	desiredPayload    []byte
	secretValue       string
	gcsUnavailable    bool
}

func newFakeRemoteServer() *fakeRemoteServer {
	desiredStateKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	nodeIdentityKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	return &fakeRemoteServer{
		desiredStateKey:   desiredStateKey,
		nodeIdentityKey:   nodeIdentityKey,
		desiredStateURI:   "gs://desired-bucket/nodes/node-a.json",
		assignmentMode:    "assigned",
		desiredSequence:   2,
		unassignedPayload: []byte(`{"revision":"unassigned-node-a","containers":[]}`),
		desiredGeneration: "7",
		desiredETag:       "etag-7",
		desiredPayload: []byte(`{
  "revision": "rev-1",
  "ingress": {
    "hostname": "abc123.devopsellence.io",
    "tunnel_token_secret_ref": "gsm://projects/test-project/secrets/API_KEY/versions/7"
  },
  "containers": [
    {
      "service_name": "worker",
      "image": "us-central1-docker.pkg.dev/devopsellence/sub-1/app:rev-1",
      "secret_refs": {
        "API_KEY": "gsm://projects/test-project/secrets/API_KEY/versions/7"
      }
    }
  ]
}`),
		secretValue: "super-secret\n",
	}
}

func (f *fakeRemoteServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/agent/bootstrap":
			f.handleBootstrap(w, r)
		case r.URL.Path == "/api/v1/agent/assignment":
			f.handleAssignment(w, r)
		case r.URL.Path == "/api/v1/agent/desired_state":
			f.handleStandaloneDesiredState(w, r)
		case r.URL.Path == desiredStateJWKSPath:
			f.handleDesiredStateJWKS(w, r)
		case r.URL.Path == "/.well-known/jwks.json":
			f.handleJWKS(w, r)
		case r.URL.Path == "/api/v1/agent/auth/refresh":
			f.handleRefresh(w, r)
		case r.URL.Path == "/api/v1/agent/sts/token":
			f.handleSubjectToken(w, r)
		case r.URL.Path == "/google/sts":
			f.handleGoogleSTS(w, r)
		case strings.HasPrefix(r.URL.Path, "/google/iam/v1/projects/-/serviceAccounts/"):
			f.handleImpersonation(w, r)
		case strings.HasPrefix(r.URL.Path, "/storage/v1/b/desired-bucket/o/"):
			f.handleGCS(w, r)
		case r.URL.Path == "/secretmanager/v1/projects/test-project/secrets/API_KEY/versions/7:access":
			f.handleSecret(w, r)
		case strings.HasPrefix(r.URL.Path, "/api/v1/agent/secrets/"):
			f.handleHTTPSecret(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

func (f *fakeRemoteServer) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bootstrapCalls++
	f.currentAccessToken = "cp-access-bootstrap"
	f.currentRefreshToken = "cp-refresh-bootstrap"
	_ = json.NewEncoder(w).Encode(map[string]any{
		"node_id":       11,
		"access_token":  f.currentAccessToken,
		"refresh_token": f.currentRefreshToken,
		"expires_in":    300,
	})
}

func (f *fakeRemoteServer) handleAssignment(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "Bearer "+f.currentAccessToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.assignmentCalls++
	if f.assignmentMode == "unassigned" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"mode":"unassigned","desired_state_sequence":%d,"desired_state":`, f.desiredSequence)))
		_, _ = w.Write(signedEnvelopeJSON(f.desiredStateKey, 11, 0, f.desiredSequence, f.unassignedPayload))
		_, _ = w.Write([]byte("}"))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"mode":                   "assigned",
		"environment_id":         44,
		"identity_version":       2,
		"desired_state_sequence": f.desiredSequence,
		"desired_state_uri":      f.desiredStateURI,
	})
}

func (f *fakeRemoteServer) handleJWKS(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]any{jwkForKey(f.nodeIdentityKey.PublicKey, "node_identity:"+fingerprint(&f.nodeIdentityKey.PublicKey))},
	})
}

func (f *fakeRemoteServer) handleDesiredStateJWKS(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]any{jwkForKey(f.desiredStateKey.PublicKey, "desired_state:"+fingerprint(&f.desiredStateKey.PublicKey))},
	})
}

func (f *fakeRemoteServer) handleRefresh(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.currentRefreshToken == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	_ = json.NewDecoder(r.Body).Decode(&map[string]any{})
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  f.currentAccessToken,
		"refresh_token": f.currentRefreshToken,
		"expires_in":    300,
	})
}

func (f *fakeRemoteServer) handleSubjectToken(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "Bearer "+f.currentAccessToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.subjectCalls++
	subjectToken := unsignedJWT(map[string]any{
		"service_account_email": "svc-1@project.iam.gserviceaccount.com",
	})
	_ = json.NewEncoder(w).Encode(map[string]any{
		"subject_token": subjectToken,
		"audience":      "//iam.googleapis.com/projects/123/locations/global/workloadIdentityPools/pool/providers/provider",
		"expires_in":    300,
	})
}

func (f *fakeRemoteServer) handleGoogleSTS(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.googleSTSCalls++
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "federated-token",
		"expires_in":   300,
	})
}

func (f *fakeRemoteServer) handleImpersonation(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "Bearer federated-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.impersonationCalls++
	_ = json.NewEncoder(w).Encode(map[string]any{
		"accessToken": "google-access-token",
		"expireTime":  time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	})
}

func (f *fakeRemoteServer) handleGCS(w http.ResponseWriter, r *http.Request) {
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "Bearer google-access-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gcsUnavailable {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "gcs unavailable"})
		return
	}
	if r.URL.Query().Get("alt") == "media" {
		f.gcsMediaCalls++
		_, _ = w.Write(signedEnvelopeJSON(f.desiredStateKey, 11, 44, f.desiredSequence, f.desiredPayload))
		return
	}

	f.gcsMetadataCalls++
	_ = json.NewEncoder(w).Encode(map[string]string{
		"generation": f.desiredGeneration,
		"etag":       f.desiredETag,
	})
}

func (f *fakeRemoteServer) handleSecret(w http.ResponseWriter, r *http.Request) {
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "Bearer google-access-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.secretCalls++
	_ = json.NewEncoder(w).Encode(map[string]any{
		"payload": map[string]string{
			"data": base64.StdEncoding.EncodeToString([]byte(f.secretValue)),
		},
	})
}

func (f *fakeRemoteServer) handleStandaloneDesiredState(w http.ResponseWriter, r *http.Request) {
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "Bearer "+f.currentAccessToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.httpDesiredCalls++
	if strings.Trim(strings.TrimSpace(r.Header.Get("If-None-Match")), "\"") == f.desiredETag {
		w.Header().Set("ETag", fmt.Sprintf("%q", f.desiredETag))
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("ETag", fmt.Sprintf("%q", f.desiredETag))
	_, _ = w.Write(signedEnvelopeJSON(f.desiredStateKey, 11, 44, f.desiredSequence, f.desiredPayload))
}

func (f *fakeRemoteServer) handleHTTPSecret(w http.ResponseWriter, r *http.Request) {
	if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "Bearer "+f.currentAccessToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.httpSecretCalls++
	_ = json.NewEncoder(w).Encode(map[string]any{
		"value": f.secretValue,
	})
}

func TestFetchReadsDesiredStateFromAssignmentLocatorAndCachesMetadata(t *testing.T) {
	serverState := newFakeRemoteServer()
	server := httptest.NewServer(serverState.handler())
	defer server.Close()

	authManager := newRemoteAuthManager(t, server.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{
		GCSAPIEndpoint:        server.URL,
		SecretManagerEndpoint: server.URL + "/secretmanager/v1",
	}, authManager, logger)

	// First fetch: full path — metadata check, media download, secret resolution.
	fetchResult, err := authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetchResult.Sequence != serverState.desiredSequence {
		t.Fatalf("unexpected fetch sequence: %d", fetchResult.Sequence)
	}
	desired := fetchResult.Desired
	if got := authManager.DesiredStateSequenceFloor(); got != serverState.desiredSequence {
		t.Fatalf("unexpected desired state sequence floor after fetch: %d", got)
	}
	if desired.Revision != "rev-1" {
		t.Fatalf("unexpected revision: %s", desired.Revision)
	}
	if got := desired.Containers[0].Env["API_KEY"]; got != "super-secret" {
		t.Fatalf("unexpected resolved secret: %q", got)
	}
	if len(desired.Containers[0].SecretRefs) != 0 {
		t.Fatal("expected secret refs to be cleared after resolution")
	}
	if desired.Ingress == nil || desired.Ingress.TunnelToken != "super-secret" {
		t.Fatalf("unexpected ingress token: %+v", desired.Ingress)
	}
	if desired.Ingress.TunnelTokenSecretRef != "" {
		t.Fatal("expected ingress secret ref to be cleared after resolution")
	}
	serverState.mu.Lock()
	if serverState.gcsMetadataCalls != 1 || serverState.gcsMediaCalls != 1 || serverState.secretCalls != 2 {
		t.Fatalf("unexpected initial fetch counts: metadata=%d media=%d secret=%d", serverState.gcsMetadataCalls, serverState.gcsMediaCalls, serverState.secretCalls)
	}
	serverState.mu.Unlock()

	// Second fetch: generation/ETag unchanged — cheap metadata check only, no re-download.
	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	serverState.mu.Lock()
	if serverState.gcsMetadataCalls != 2 || serverState.gcsMediaCalls != 1 || serverState.secretCalls != 2 {
		t.Fatalf("expected cheap second fetch: metadata=%d media=%d secret=%d", serverState.gcsMetadataCalls, serverState.gcsMediaCalls, serverState.secretCalls)
	}
	serverState.mu.Unlock()

	// Third fetch after generation changes: full download triggered.
	serverState.mu.Lock()
	serverState.desiredGeneration = "8"
	serverState.desiredETag = "etag-8"
	serverState.mu.Unlock()

	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("third fetch: %v", err)
	}
	serverState.mu.Lock()
	defer serverState.mu.Unlock()
	if serverState.gcsMetadataCalls != 3 {
		t.Fatalf("expected metadata recheck, got %d", serverState.gcsMetadataCalls)
	}
	if serverState.gcsMediaCalls != 2 {
		t.Fatalf("expected media re-fetch on generation change, got %d", serverState.gcsMediaCalls)
	}
	if serverState.secretCalls != 4 {
		t.Fatalf("expected secrets re-fetched on generation change, got %d", serverState.secretCalls)
	}
}

func TestFetchReadsDesiredStateOverControlPlaneHTTPAndCachesETag(t *testing.T) {
	serverState := newFakeRemoteServer()
	server := httptest.NewServer(serverState.handler())
	defer server.Close()

	serverState.mu.Lock()
	serverState.desiredStateURI = server.URL + "/api/v1/agent/desired_state"
	serverState.desiredPayload = []byte(fmt.Sprintf(`{
  "revision": "rev-http",
  "ingress": {
    "hostname": "abc123.devopsellence.io",
    "tunnel_token_secret_ref": "%s/api/v1/agent/secrets/environment_bundles/1/tunnel_token"
  },
  "containers": [
    {
      "service_name": "worker",
      "image": "ghcr.io/example/app:rev-http",
      "secret_refs": {
        "API_KEY": "%s/api/v1/agent/secrets/environment_secrets/1"
      }
    }
  ]
}`, server.URL, server.URL))
	serverState.mu.Unlock()

	authManager := newRemoteAuthManager(t, server.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{}, authManager, logger)

	fetchResult, err := authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetchResult.Sequence != serverState.desiredSequence {
		t.Fatalf("unexpected fetch sequence: %d", fetchResult.Sequence)
	}
	if got := fetchResult.Desired.Containers[0].Env["API_KEY"]; got != "super-secret" {
		t.Fatalf("unexpected resolved secret: %q", got)
	}
	if fetchResult.Desired.Ingress == nil || fetchResult.Desired.Ingress.TunnelToken != "super-secret" {
		t.Fatalf("unexpected ingress token: %+v", fetchResult.Desired.Ingress)
	}

	serverState.mu.Lock()
	if serverState.httpDesiredCalls != 1 || serverState.httpSecretCalls != 2 || serverState.googleSTSCalls != 0 || serverState.secretCalls != 0 {
		t.Fatalf("unexpected initial http fetch counts: desired=%d cp_secret=%d google_sts=%d gsm_secret=%d", serverState.httpDesiredCalls, serverState.httpSecretCalls, serverState.googleSTSCalls, serverState.secretCalls)
	}
	serverState.mu.Unlock()

	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("second fetch: %v", err)
	}

	serverState.mu.Lock()
	defer serverState.mu.Unlock()
	if serverState.httpDesiredCalls != 2 {
		t.Fatalf("expected etag recheck on second fetch, got %d", serverState.httpDesiredCalls)
	}
	if serverState.httpSecretCalls != 2 {
		t.Fatalf("expected no secret ref re-fetch on not modified, got %d", serverState.httpSecretCalls)
	}
	if serverState.googleSTSCalls != 0 || serverState.secretCalls != 0 {
		t.Fatalf("expected no google secret path calls, got google_sts=%d gsm_secret=%d", serverState.googleSTSCalls, serverState.secretCalls)
	}
}

func TestFetchReadsInlineDesiredStateWhileUnassigned(t *testing.T) {
	serverState := newFakeRemoteServer()
	serverState.assignmentMode = "unassigned"
	server := httptest.NewServer(serverState.handler())
	defer server.Close()

	authManager := newRemoteAuthManager(t, server.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	remoteAuthority := New(Config{}, authManager, logger)

	fetchResult, err := remoteAuthority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fetchResult.Sequence != serverState.desiredSequence {
		t.Fatalf("unexpected fetch sequence: %d", fetchResult.Sequence)
	}
	desired := fetchResult.Desired
	if desired.Revision != "unassigned-node-a" {
		t.Fatalf("unexpected revision: %s", desired.Revision)
	}
	if len(desired.Containers) != 0 {
		t.Fatalf("expected empty container list, got %d", len(desired.Containers))
	}
}

func TestFetchRejectsDesiredStateReplay(t *testing.T) {
	serverState := newFakeRemoteServer()
	server := httptest.NewServer(serverState.handler())
	defer server.Close()

	authManager := newRemoteAuthManager(t, server.URL)
	if err := authManager.RecordDesiredStateSequenceFloor(serverState.desiredSequence + 1); err != nil {
		t.Fatalf("record desired state sequence floor: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{
		GCSAPIEndpoint:        server.URL,
		SecretManagerEndpoint: server.URL + "/secretmanager/v1",
	}, authManager, logger)

	if _, err := authority.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "sequence rollback") {
		t.Fatalf("expected sequence rollback error, got %v", err)
	}
}

func TestFetchUsesSingleAssignmentSnapshotPerFetch(t *testing.T) {
	serverState := newFakeRemoteServer()
	serverState.assignmentMode = "unassigned"
	serverState.desiredSequence = 0
	server := httptest.NewServer(serverState.handler())
	defer server.Close()

	authManager := newRemoteAuthManager(t, server.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{
		GCSAPIEndpoint:        server.URL,
		SecretManagerEndpoint: server.URL + "/secretmanager/v1",
	}, authManager, logger)

	var once sync.Once
	authority.beforeFetchForTest = func() {
		once.Do(func() {
			serverState.mu.Lock()
			serverState.assignmentMode = "assigned"
			serverState.desiredSequence = 1
			serverState.mu.Unlock()
			if err := authManager.Sync(context.Background()); err != nil {
				t.Fatalf("sync auth manager: %v", err)
			}
		})
	}

	fetchResult, err := authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch with mid-flight assignment flip: %v", err)
	}
	desired := fetchResult.Desired
	if desired.Revision != "unassigned-node-a" {
		t.Fatalf("expected fetch to honor initial unassigned snapshot, got %q", desired.Revision)
	}
	if got := authManager.DesiredStateSequenceFloor(); got != 1 {
		t.Fatalf("expected assignment sync to advance floor to 1, got %d", got)
	}

	fetchResult, err = authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch after assignment flip: %v", err)
	}
	desired = fetchResult.Desired
	if desired.Revision != "rev-1" {
		t.Fatalf("expected follow-up fetch to read assigned desired state, got %q", desired.Revision)
	}
}

func TestFetchRejectsCachedDesiredStateBelowSequenceFloor(t *testing.T) {
	serverState := newFakeRemoteServer()
	serverState.desiredSequence = 2
	server := httptest.NewServer(serverState.handler())
	defer server.Close()

	authManager := newRemoteAuthManager(t, server.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{
		GCSAPIEndpoint:        server.URL,
		SecretManagerEndpoint: server.URL + "/secretmanager/v1",
	}, authManager, logger)

	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("initial fetch: %v", err)
	}
	if err := authManager.RecordDesiredStateSequenceFloor(3); err != nil {
		t.Fatalf("advance desired state sequence floor: %v", err)
	}

	if _, err := authority.Fetch(context.Background()); err == nil || !strings.Contains(err.Error(), "sequence rollback") {
		t.Fatalf("expected cached desired state rollback error, got %v", err)
	}
}

func TestFetchFallsBackToPersistedDesiredStateCacheWhenGCSUnavailable(t *testing.T) {
	serverState := newFakeRemoteServer()
	server := httptest.NewServer(serverState.handler())

	authManager := newRemoteAuthManager(t, server.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	cachePath := filepath.Join(t.TempDir(), "desired-state-cache.json")
	authority := New(Config{
		GCSAPIEndpoint:        server.URL,
		SecretManagerEndpoint: server.URL + "/secretmanager/v1",
		DesiredStateCachePath: cachePath,
	}, authManager, logger)

	fetchResult, err := authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("initial fetch: %v", err)
	}
	desired := fetchResult.Desired
	if desired.Revision != "rev-1" {
		t.Fatalf("unexpected revision: %s", desired.Revision)
	}
	server.Close()

	fetchResult, err = authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch from persisted cache: %v", err)
	}
	desired = fetchResult.Desired
	if desired.Revision != "rev-1" {
		t.Fatalf("unexpected cached revision: %s", desired.Revision)
	}
}

func TestFetchFallsBackToStaleDesiredStateCacheWhenSourceUnavailable(t *testing.T) {
	serverState := newFakeRemoteServer()
	server := httptest.NewServer(serverState.handler())

	authManager := newRemoteAuthManager(t, server.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	cachePath := filepath.Join(t.TempDir(), "desired-state-cache.json")
	authority := New(Config{
		GCSAPIEndpoint:        server.URL,
		SecretManagerEndpoint: server.URL + "/secretmanager/v1",
		DesiredStateCachePath: cachePath,
	}, authManager, logger)

	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("initial fetch: %v", err)
	}
	if err := authManager.RecordDesiredStateSequenceFloor(serverState.desiredSequence + 1); err != nil {
		t.Fatalf("advance desired state sequence floor: %v", err)
	}
	server.Close()

	fetchResult, err := authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch from stale cache: %v", err)
	}
	desired := fetchResult.Desired
	if desired.Revision != "rev-1" {
		t.Fatalf("unexpected cached revision: %s", desired.Revision)
	}
}

func TestFetchUsesLocalDesiredStateOverrideWhenPresent(t *testing.T) {
	overridePath := filepath.Join(t.TempDir(), "desired-state-override.json")
	if err := os.WriteFile(overridePath, []byte(`{"enabled":true,"desired_state":{"revision":"manual-rev","containers":[]}}`), 0o600); err != nil {
		t.Fatalf("write override: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{
		DesiredStateOverridePath: overridePath,
	}, nil, logger)

	fetchResult, err := authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch override: %v", err)
	}
	desired := fetchResult.Desired
	if desired.Revision != "manual-rev" {
		t.Fatalf("unexpected override revision: %s", desired.Revision)
	}
}

func TestFetchReadsDesiredStateThroughPointerObject(t *testing.T) {
	serverState := newFakeRemoteServer()
	serverState.desiredStateURI = "gs://desired-bucket/nodes/node-a/desired_state.json"
	serverState.desiredSequence = 2
	authServer := httptest.NewServer(serverState.handler())
	defer authServer.Close()

	pointerObjectPath := "nodes/node-a/desired_state.json"
	immutableObjectPath := "nodes/node-a/desired-states/000000000002.json"
	pointerMetadataCalls := 0
	pointerMediaCalls := 0
	immutableMediaCalls := 0
	gcsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "Bearer google-access-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		object := strings.TrimPrefix(r.URL.Path, "/storage/v1/b/desired-bucket/o/")
		switch {
		case r.URL.Query().Get("alt") == "media" && object == pointerObjectPath:
			pointerMediaCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"format":         desiredStatePointerFmt,
				"schema_version": 1,
				"sequence":       serverState.desiredSequence,
				"object_path":    immutableObjectPath,
				"published_at":   time.Now().UTC().Format(time.RFC3339),
			})
		case r.URL.Query().Get("alt") == "media" && object == immutableObjectPath:
			immutableMediaCalls++
			_, _ = w.Write(signedEnvelopeJSON(serverState.desiredStateKey, 11, 44, serverState.desiredSequence, []byte(`{"revision":"pointer-rev","containers":[]}`)))
		case object == pointerObjectPath:
			pointerMetadataCalls++
			_ = json.NewEncoder(w).Encode(map[string]string{
				"generation": "7",
				"etag":       "etag-7",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer gcsServer.Close()

	authManager := newRemoteAuthManager(t, authServer.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{
		GCSAPIEndpoint: gcsServer.URL,
	}, authManager, logger)

	fetchResult, err := authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch through pointer: %v", err)
	}
	desired := fetchResult.Desired
	if desired.Revision != "pointer-rev" {
		t.Fatalf("unexpected revision: %s", desired.Revision)
	}
	if pointerMetadataCalls != 1 || pointerMediaCalls != 1 || immutableMediaCalls != 1 {
		t.Fatalf("unexpected first fetch counts: metadata=%d pointer_media=%d immutable_media=%d", pointerMetadataCalls, pointerMediaCalls, immutableMediaCalls)
	}

	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("second fetch through pointer: %v", err)
	}
	if pointerMetadataCalls != 2 || pointerMediaCalls != 1 || immutableMediaCalls != 1 {
		t.Fatalf("expected cached second fetch: metadata=%d pointer_media=%d immutable_media=%d", pointerMetadataCalls, pointerMediaCalls, immutableMediaCalls)
	}
}

func TestFetchThrottlesFallbackLogsUntilRecovery(t *testing.T) {
	serverState := newFakeRemoteServer()
	server := httptest.NewServer(serverState.handler())
	defer server.Close()

	authManager := newRemoteAuthManager(t, server.URL)
	var logs strings.Builder
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{}))
	authority := New(Config{
		GCSAPIEndpoint:        server.URL,
		SecretManagerEndpoint: server.URL + "/secretmanager/v1",
	}, authManager, logger)

	now := time.Date(2026, 3, 27, 8, 0, 0, 0, time.UTC)
	authority.now = func() time.Time { return now }

	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("initial fetch: %v", err)
	}

	serverState.mu.Lock()
	serverState.gcsUnavailable = true
	serverState.mu.Unlock()

	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("first fallback fetch: %v", err)
	}
	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("second fallback fetch: %v", err)
	}
	now = now.Add(30 * time.Second)
	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("third fallback fetch: %v", err)
	}
	now = now.Add(31 * time.Second)
	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("fourth fallback fetch: %v", err)
	}

	serverState.mu.Lock()
	serverState.gcsUnavailable = false
	serverState.mu.Unlock()
	now = now.Add(time.Second)
	if _, err := authority.Fetch(context.Background()); err != nil {
		t.Fatalf("recovery fetch: %v", err)
	}

	output := logs.String()
	if got := strings.Count(output, "using in-memory desired state cache"); got != 2 {
		t.Fatalf("expected 2 throttled fallback logs, got %d\n%s", got, output)
	}
	if got := strings.Count(output, "authoritative desired state source restored"); got != 1 {
		t.Fatalf("expected 1 recovery log, got %d\n%s", got, output)
	}
}

func TestResolveSecretRefsRejectsUnsupportedScheme(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{}, nil, logger)

	desired := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Containers: []*desiredstatepb.Container{{
			ServiceName: "worker",
			Image:       "busybox",
			SecretRefs: map[string]string{
				"API_KEY": "file://secrets/API_KEY",
			},
		}},
	}

	if err := authority.resolveSecretRefs(context.Background(), desired, "google-access-token", ""); err == nil {
		t.Fatal("expected unsupported scheme error")
	}
}

func TestResolveSecretRefsRejectsEnvConflict(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{}, nil, logger)

	desired := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Containers: []*desiredstatepb.Container{{
			ServiceName: "worker",
			Image:       "busybox",
			Env:         map[string]string{"API_KEY": "inline"},
			SecretRefs: map[string]string{
				"API_KEY": "gsm://projects/test-project/secrets/API_KEY/versions/7",
			},
		}},
	}

	if err := authority.resolveSecretRefs(context.Background(), desired, "google-access-token", ""); err == nil {
		t.Fatal("expected env conflict error")
	}
}

// Regression test: warm server (node_id=11) reads a bundle envelope (node_id=0)
// with matching bundle tokens. Before the fix, fetchAssignmentLocked dropped
// bundle tokens so the node_id check triggered: "got 0 want 11".
func TestFetchAcceptsBundleEnvelopeWithNodeIDZeroWhenBundleTokensMatch(t *testing.T) {
	serverState := newFakeRemoteServer()

	// Override assignment to return bundle tokens (simulating DesiredStateTarget.for)
	originalHandler := serverState.handler()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/assignment", func(w http.ResponseWriter, r *http.Request) {
		serverState.mu.Lock()
		defer serverState.mu.Unlock()
		if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "Bearer "+serverState.currentAccessToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		serverState.assignmentCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"mode":                      "assigned",
			"environment_id":            44,
			"identity_version":          2,
			"desired_state_sequence":    serverState.desiredSequence,
			"desired_state_uri":         serverState.desiredStateURI,
			"organization_bundle_token": "orgb-1",
			"environment_bundle_token":  "envb-1",
			"node_bundle_token":         "nodeb-1",
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		originalHandler.ServeHTTP(w, r)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Override GCS to return a bundle envelope with node_id=0 (from publish_for_bundle!)
	// but with matching bundle tokens
	bundlePayload := []byte(`{"revision":"unassigned-node-bundle-nodeb-1","containers":[]}`)

	gcsHandler := http.NewServeMux()
	gcsHandler.HandleFunc("/storage/v1/b/desired-bucket/o/", func(w http.ResponseWriter, r *http.Request) {
		if authz := strings.TrimSpace(r.Header.Get("Authorization")); authz != "Bearer google-access-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		serverState.mu.Lock()
		defer serverState.mu.Unlock()
		if r.URL.Query().Get("alt") == "media" {
			serverState.gcsMediaCalls++
			// Bundle envelope: node_id=0, environment_id=0, with bundle tokens
			_, _ = w.Write(signedEnvelopeJSONWithBundleTokens(
				serverState.desiredStateKey, 0, 0, serverState.desiredSequence,
				bundlePayload, "orgb-1", "envb-1", "nodeb-1"))
			return
		}
		serverState.gcsMetadataCalls++
		_ = json.NewEncoder(w).Encode(map[string]string{
			"generation": serverState.desiredGeneration,
			"etag":       serverState.desiredETag,
		})
	})
	gcsHandler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		originalHandler.ServeHTTP(w, r)
	})
	gcsServer := httptest.NewServer(gcsHandler)
	defer gcsServer.Close()

	// Wire up: auth against main server, GCS against gcsServer
	authManager := newRemoteAuthManager(t, server.URL)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	authority := New(Config{
		GCSAPIEndpoint: gcsServer.URL,
	}, authManager, logger)

	// This should succeed — bundle tokens match, node_id check is skipped
	fetchResult, err := authority.Fetch(context.Background())
	if err != nil {
		t.Fatalf("fetch should succeed with bundle tokens despite node_id=0, got: %v", err)
	}
	desired := fetchResult.Desired
	if desired.Revision != "unassigned-node-bundle-nodeb-1" {
		t.Fatalf("unexpected revision: %s", desired.Revision)
	}
}

func newRemoteAuthManager(t *testing.T, baseURL string) *auth.Manager {
	t.Helper()

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	manager, err := auth.NewManager(auth.Config{
		BaseURL:                      baseURL,
		BootstrapToken:               "bootstrap-token",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            baseURL + "/google/sts",
		GoogleIAMCredentialsEndpoint: baseURL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new auth manager: %v", err)
	}
	if err := manager.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize auth manager: %v", err)
	}
	return manager
}

func unsignedJWT(claims map[string]any) string {
	headerJSON, _ := json.Marshal(map[string]any{"alg": "none", "typ": "JWT"})
	claimsJSON, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON) + ".sig"
}

func signedEnvelopeJSON(key *rsa.PrivateKey, nodeID, environmentID, sequence int64, payload []byte) []byte {
	return signedEnvelopeJSONWithBundleTokens(key, nodeID, environmentID, sequence, payload, "", "", "")
}

func signedEnvelopeJSONWithBundleTokens(key *rsa.PrivateKey, nodeID, environmentID, sequence int64, payload []byte, orgBundleToken, envBundleToken, nodeBundleToken string) []byte {
	timestamp := time.Now().UTC()
	expiresAt := timestamp.Add(24 * time.Hour)
	payloadSHA := sha256.Sum256(payload)
	signingInput := buildDesiredStateSigningInput(orgBundleToken, envBundleToken, nodeBundleToken, nodeID, environmentID, sequence, timestamp, expiresAt, fmt.Sprintf("%x", payloadSHA[:]))
	digest := sha256.Sum256([]byte(signingInput))
	signature, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])

	body, _ := json.Marshal(map[string]any{
		"format":                    desiredStateEnvelopeFmt,
		"schema_version":            1,
		"algorithm":                 "RS256",
		"key_id":                    "desired_state:" + fingerprint(&key.PublicKey),
		"organization_bundle_token": orgBundleToken,
		"environment_bundle_token":  envBundleToken,
		"node_bundle_token":         nodeBundleToken,
		"node_id":                   nodeID,
		"environment_id":            environmentID,
		"sequence":                  sequence,
		"issued_at":                 timestamp.Format(time.RFC3339),
		"expires_at":                expiresAt.Format(time.RFC3339),
		"payload_sha256":            fmt.Sprintf("%x", payloadSHA[:]),
		"payload_json":              string(payload),
		"signature":                 base64.RawURLEncoding.EncodeToString(signature),
	})
	return body
}

func jwkForKey(key rsa.PublicKey, kid string) map[string]any {
	return map[string]any{
		"kty": "RSA",
		"alg": "RS256",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
	}
}

func fingerprint(key *rsa.PublicKey) string {
	return fmt.Sprintf("%x", sha256.Sum256(x509MarshalPKCS1PublicKey(key)))[:16]
}

func x509MarshalPKCS1PublicKey(key *rsa.PublicKey) []byte {
	return x509.MarshalPKCS1PublicKey(key)
}
