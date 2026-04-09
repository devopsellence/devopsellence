package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/version"
)

type fakeAuthServer struct {
	mu sync.Mutex

	bootstrapCalls      int
	assignmentCalls     int
	refreshCalls        int
	subjectCalls        int
	googleSTSCalls      int
	impersonationCalls  int
	issuedAccessCounter int
	issuedGoogleCounter int
	impersonationErrors []int

	currentAccessToken   string
	currentRefresh       string
	desiredStateURI      string
	desiredStateTarget   bool
	assignmentMode       string
	desiredStateSequence int64

	assignmentBundleTokens bool
	bootstrapCapabilities  string
	assignmentCapabilities string
	refreshCapabilities    string
	bootstrapProviderID    string

	controlPlaneAccessTTL time.Duration
	googleAccessTTL       time.Duration
}

func newFakeAuthServer(controlPlaneAccessTTL, googleAccessTTL time.Duration) *fakeAuthServer {
	return &fakeAuthServer{
		controlPlaneAccessTTL: controlPlaneAccessTTL,
		googleAccessTTL:       googleAccessTTL,
		desiredStateURI:       "gs://desired-state-bucket/nodes/node-a.json",
		assignmentMode:        "assigned",
		desiredStateSequence:  7,
	}
}

func (f *fakeAuthServer) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc(bootstrapPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.TrimSpace(fmt.Sprintf("%v", req["bootstrap_token"])) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing bootstrap_token"})
			return
		}

		f.mu.Lock()
		defer f.mu.Unlock()
		f.bootstrapCalls++
		f.bootstrapCapabilities = strings.TrimSpace(r.Header.Get(version.CapabilitiesHeader))
		f.bootstrapProviderID = strings.TrimSpace(fmt.Sprintf("%v", req["provider_server_id"]))
		f.issuedAccessCounter++
		f.currentAccessToken = fmt.Sprintf("cp_access_bootstrap_%d", f.issuedAccessCounter)
		f.currentRefresh = "cp_refresh_bootstrap"
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":         f.currentAccessToken,
			"refresh_token":        f.currentRefresh,
			"expires_in":           int64(f.controlPlaneAccessTTL / time.Second),
			"desired_state_target": f.desiredStateTargetResponse(),
		})
	})

	mux.HandleFunc(assignmentPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		authorization := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(authorization, "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing bearer"})
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))

		f.mu.Lock()
		defer f.mu.Unlock()
		if token != f.currentAccessToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid access"})
			return
		}
		f.assignmentCalls++
		f.assignmentCapabilities = strings.TrimSpace(r.Header.Get(version.CapabilitiesHeader))
		if f.assignmentMode == "unassigned" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"mode":                   "unassigned",
				"desired_state_sequence": f.desiredStateSequence,
				"desired_state":          map[string]any{"revision": "unassigned-node-a", "containers": []any{}},
			})
			return
		}
		resp := map[string]any{
			"mode":                   "assigned",
			"environment_id":         42,
			"identity_version":       3,
			"desired_state_sequence": f.desiredStateSequence,
			"desired_state_uri":      f.desiredStateURI,
		}
		if f.assignmentBundleTokens {
			resp["organization_bundle_token"] = "orgb-1"
			resp["environment_bundle_token"] = "envb-1"
			resp["node_bundle_token"] = "nodeb-1"
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	mux.HandleFunc(refreshPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		refreshToken := strings.TrimSpace(fmt.Sprintf("%v", req["refresh_token"]))
		if refreshToken == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing refresh_token"})
			return
		}

		f.mu.Lock()
		defer f.mu.Unlock()
		if refreshToken != f.currentRefresh {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid refresh"})
			return
		}
		f.refreshCalls++
		f.refreshCapabilities = strings.TrimSpace(r.Header.Get(version.CapabilitiesHeader))
		f.issuedAccessCounter++
		f.currentAccessToken = fmt.Sprintf("cp_access_refresh_%d", f.issuedAccessCounter)
		f.currentRefresh = fmt.Sprintf("cp_refresh_%d", f.refreshCalls)

		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":         f.currentAccessToken,
			"refresh_token":        f.currentRefresh,
			"expires_in":           int64(f.controlPlaneAccessTTL / time.Second),
			"desired_state_target": f.desiredStateTargetResponse(),
		})
	})

	mux.HandleFunc(stsPath, func(w http.ResponseWriter, r *http.Request) {
		authorization := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(authorization, "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing bearer"})
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer "))

		f.mu.Lock()
		defer f.mu.Unlock()
		if token != f.currentAccessToken {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid access"})
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
	})

	mux.HandleFunc("/google/sts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.TrimSpace(fmt.Sprintf("%v", req["subjectToken"])) == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing subjectToken"})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.googleSTSCalls++
		f.issuedGoogleCounter++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": fmt.Sprintf("federated_%d", f.issuedGoogleCounter),
			"expires_in":   int64(30),
		})
	})

	mux.HandleFunc("/google/iam/v1/projects/-/serviceAccounts/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		authorization := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(authorization, "Bearer federated_") {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid federated bearer"})
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.impersonationCalls++
		if len(f.impersonationErrors) > 0 {
			status := f.impersonationErrors[0]
			f.impersonationErrors = f.impersonationErrors[1:]
			w.WriteHeader(status)
			if status == http.StatusForbidden {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]any{
						"status":  "PERMISSION_DENIED",
						"message": "Permission 'iam.serviceAccounts.getAccessToken' denied on resource (or it may not exist).",
						"details": []map[string]any{
							{
								"reason": "IAM_PERMISSION_DENIED",
								"metadata": map[string]any{
									"permission": "iam.serviceAccounts.getAccessToken",
								},
							},
						},
					},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"status":  "NOT_FOUND",
					"message": "Not found; Gaia id not found for email svc-1@project.iam.gserviceaccount.com",
				},
			})
			return
		}
		f.issuedGoogleCounter++
		expireAt := time.Now().UTC().Add(f.googleAccessTTL).Format(time.RFC3339)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accessToken": fmt.Sprintf("gcp_access_%d", f.issuedGoogleCounter),
			"expireTime":  expireAt,
		})
	})

	return mux
}

func (f *fakeAuthServer) desiredStateTargetResponse() map[string]any {
	if !f.desiredStateTarget {
		return nil
	}
	return map[string]any{
		"mode":                      "managed_bundle",
		"desired_state_sequence":    f.desiredStateSequence,
		"desired_state_uri":         f.desiredStateURI,
		"organization_bundle_token": "orgb-1",
		"environment_bundle_token":  "envb-1",
		"node_bundle_token":         "nodeb-1",
	}
}

func TestManagerInitializeBootstrapsAndMintsGoogleAccessToken(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		AuthCheckInterval:            50 * time.Millisecond,
		TokenRefreshSkew:             10 * time.Second,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	target := m.DesiredStateTarget()
	if target.URI != fake.desiredStateURI {
		t.Fatalf("unexpected desired state uri: %s", target.URI)
	}
	if target.Mode != "assigned" {
		t.Fatalf("unexpected assignment mode: %s", target.Mode)
	}
	if got := m.DesiredStateSequenceFloor(); got != fake.desiredStateSequence {
		t.Fatalf("unexpected desired state sequence floor: %d", got)
	}

	googleToken, googleExpiry, err := m.GoogleAccess(context.Background())
	if err != nil {
		t.Fatalf("google access: %v", err)
	}

	cpToken, cpExpiry := m.ControlPlaneAccessToken()
	if cpToken == "" || cpExpiry.IsZero() {
		t.Fatalf("expected control plane token, got %q %v", cpToken, cpExpiry)
	}
	if googleToken == "" || googleExpiry.IsZero() {
		t.Fatalf("expected google token, got %q %v", googleToken, googleExpiry)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.bootstrapCalls != 1 {
		t.Fatalf("expected bootstrap once, got %d", fake.bootstrapCalls)
	}
	if fake.assignmentCalls != 1 {
		t.Fatalf("expected assignment poll once, got %d", fake.assignmentCalls)
	}
	if fake.bootstrapCapabilities != version.CapabilityHeaderValue() {
		t.Fatalf("unexpected bootstrap capabilities header: %q", fake.bootstrapCapabilities)
	}
	if fake.assignmentCapabilities != version.CapabilityHeaderValue() {
		t.Fatalf("unexpected assignment capabilities header: %q", fake.assignmentCapabilities)
	}
	if fake.subjectCalls != 1 || fake.googleSTSCalls != 1 || fake.impersonationCalls != 1 {
		t.Fatalf("expected sts chain once, got subject=%d google_sts=%d impersonation=%d", fake.subjectCalls, fake.googleSTSCalls, fake.impersonationCalls)
	}
}

func TestManagerInitializeSendsCloudInitInstanceIDDuringBootstrap(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	tempDir := t.TempDir()
	instanceDataPath := filepath.Join(tempDir, "instance-data.json")
	if err := os.WriteFile(instanceDataPath, []byte(`{"v1":{"instance_id":"srv-123"}}`), 0o600); err != nil {
		t.Fatalf("write instance data: %v", err)
	}

	statePath := filepath.Join(tempDir, "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		CloudInitInstanceDataPath:    instanceDataPath,
		StatePath:                    statePath,
		AuthCheckInterval:            50 * time.Millisecond,
		TokenRefreshSkew:             10 * time.Second,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.bootstrapProviderID != "srv-123" {
		t.Fatalf("expected bootstrap provider server id srv-123, got %q", fake.bootstrapProviderID)
	}
}

func TestManagerRunRefreshesTokensPeriodically(t *testing.T) {
	fake := newFakeAuthServer(1*time.Second, 1*time.Second)
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		AuthCheckInterval:            100 * time.Millisecond,
		TokenRefreshSkew:             300 * time.Millisecond,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, _, err := m.GoogleAccess(context.Background()); err != nil {
		t.Fatalf("google access: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2200*time.Millisecond)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- m.Run(ctx)
	}()

	err = <-errCh
	if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("run: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.refreshCalls < 1 {
		t.Fatalf("expected at least one control-plane refresh, got %d", fake.refreshCalls)
	}
	if fake.refreshCapabilities != version.CapabilityHeaderValue() {
		t.Fatalf("unexpected refresh capabilities header: %q", fake.refreshCapabilities)
	}
	if fake.assignmentCalls < 2 {
		t.Fatalf("expected repeated assignment polls, got %d", fake.assignmentCalls)
	}
	if fake.googleSTSCalls < 2 {
		t.Fatalf("expected repeated google sts exchange, got %d", fake.googleSTSCalls)
	}
	if fake.impersonationCalls < 2 {
		t.Fatalf("expected repeated impersonation calls, got %d", fake.impersonationCalls)
	}
}

func TestManagerInitializeUsesManagedDesiredStateTargetWithoutAssignmentPoll(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	fake.desiredStateTarget = true
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		AuthCheckInterval:            50 * time.Millisecond,
		TokenRefreshSkew:             10 * time.Second,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	target := m.DesiredStateTarget()
	if target.Mode != "managed_bundle" {
		t.Fatalf("unexpected desired state mode: %s", target.Mode)
	}
	if target.NodeBundleToken != "nodeb-1" {
		t.Fatalf("unexpected node bundle token: %s", target.NodeBundleToken)
	}
	if target.URI != fake.desiredStateURI {
		t.Fatalf("unexpected desired state uri: %s", target.URI)
	}
	if _, _, err := m.GoogleAccess(context.Background()); err != nil {
		t.Fatalf("google access: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.assignmentCalls != 0 {
		t.Fatalf("expected no assignment polls for managed target, got %d", fake.assignmentCalls)
	}
	if fake.subjectCalls != 1 || fake.googleSTSCalls != 1 || fake.impersonationCalls != 1 {
		t.Fatalf("expected sts chain once, got subject=%d google_sts=%d impersonation=%d", fake.subjectCalls, fake.googleSTSCalls, fake.impersonationCalls)
	}
}

func TestManagerInitializeResetsSequenceFloorWhenAssignmentChanges(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if got := m.DesiredStateSequenceFloor(); got != 7 {
		t.Fatalf("unexpected initial sequence floor: %d", got)
	}
	if err := m.RecordDesiredStateSequenceFloor(9); err != nil {
		t.Fatalf("record desired state sequence floor: %v", err)
	}
	if got := m.DesiredStateSequenceFloor(); got != 9 {
		t.Fatalf("unexpected recorded sequence floor: %d", got)
	}

	fake.mu.Lock()
	fake.assignmentMode = "unassigned"
	fake.desiredStateSequence = 2
	fake.mu.Unlock()

	if err := m.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := m.DesiredStateSequenceFloor(); got != 2 {
		t.Fatalf("expected reset sequence floor on unassign, got %d", got)
	}
}

func TestManagerSyncRealignsUnassignedSequenceFloorWhenItDrops(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	fake.assignmentMode = "unassigned"
	fake.desiredStateSequence = 2
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := m.RecordDesiredStateSequenceFloor(5); err != nil {
		t.Fatalf("record desired state sequence floor: %v", err)
	}

	fake.mu.Lock()
	fake.desiredStateSequence = 0
	fake.mu.Unlock()

	if err := m.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := m.DesiredStateSequenceFloor(); got != 0 {
		t.Fatalf("expected unassigned floor reset to 0, got %d", got)
	}
}

func TestManagerRecordDesiredStateSequenceFloorForSnapshotIgnoresStaleSnapshot(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	snapshot := m.DesiredStateSnapshot()

	fake.mu.Lock()
	fake.assignmentMode = "unassigned"
	fake.desiredStateSequence = 0
	fake.mu.Unlock()

	if err := m.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := m.RecordDesiredStateSequenceFloorForSnapshot(snapshot, 7); err != nil {
		t.Fatalf("record desired state sequence floor for snapshot: %v", err)
	}
	if got := m.DesiredStateSequenceFloor(); got != 0 {
		t.Fatalf("expected stale snapshot not to raise floor, got %d", got)
	}
}

func TestManagerGoogleAccessRetriesTransientImpersonationFailures(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	fake.impersonationErrors = []int{http.StatusForbidden, http.StatusForbidden}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		AuthCheckInterval:            50 * time.Millisecond,
		TokenRefreshSkew:             10 * time.Second,
		GoogleIAMRetryDelays:         []time.Duration{0, 0},
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	googleToken, _, err := m.GoogleAccess(context.Background())
	if err != nil {
		t.Fatalf("google access: %v", err)
	}
	if googleToken == "" {
		t.Fatal("expected google access token after retries")
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.impersonationCalls != 3 {
		t.Fatalf("expected 3 impersonation attempts, got %d", fake.impersonationCalls)
	}
}

func TestManagerAssignmentPropagatesBundleTokens(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	fake.assignmentBundleTokens = true
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		AuthCheckInterval:            50 * time.Millisecond,
		TokenRefreshSkew:             10 * time.Second,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	target := m.DesiredStateTarget()
	if target.Mode != "assigned" {
		t.Fatalf("unexpected mode: %s", target.Mode)
	}
	if target.NodeBundleToken != "nodeb-1" {
		t.Fatalf("expected node_bundle_token 'nodeb-1' from assignment, got %q", target.NodeBundleToken)
	}
	if target.OrganizationBundleToken != "orgb-1" {
		t.Fatalf("expected organization_bundle_token 'orgb-1' from assignment, got %q", target.OrganizationBundleToken)
	}
	if target.EnvironmentBundleToken != "envb-1" {
		t.Fatalf("expected environment_bundle_token 'envb-1' from assignment, got %q", target.EnvironmentBundleToken)
	}
}

func TestManagerAssignmentResetsSequenceFloorOnBundleTokenChange(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	fake.assignmentBundleTokens = true
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if got := m.DesiredStateSequenceFloor(); got != 7 {
		t.Fatalf("unexpected initial sequence floor: %d", got)
	}
	if err := m.RecordDesiredStateSequenceFloor(12); err != nil {
		t.Fatalf("record desired state sequence floor: %v", err)
	}

	// Simulate reassignment to a different bundle with a lower sequence
	fake.mu.Lock()
	fake.desiredStateSequence = 3
	fake.mu.Unlock()

	if err := m.Sync(context.Background()); err != nil {
		t.Fatalf("sync (same bundle): %v", err)
	}
	// Same bundle tokens, sequence floor should only increase
	if got := m.DesiredStateSequenceFloor(); got != 12 {
		t.Fatalf("expected floor to stay at 12 for same bundle, got %d", got)
	}

	// Now change the bundle tokens (simulating reassignment to a different bundle)
	fake.mu.Lock()
	fake.assignmentBundleTokens = false // removes bundle tokens (different assignment)
	fake.desiredStateSequence = 3
	fake.mu.Unlock()

	if err := m.Sync(context.Background()); err != nil {
		t.Fatalf("sync (changed bundle): %v", err)
	}
	// Bundle tokens changed, so sequence floor should reset
	if got := m.DesiredStateSequenceFloor(); got != 3 {
		t.Fatalf("expected floor reset to 3 on bundle change, got %d", got)
	}
}

func TestManagerNextCheckIntervalUsesFastPollingWhileUnassigned(t *testing.T) {
	m := &Manager{
		cfg: Config{
			AuthCheckInterval: 30 * time.Second,
		},
	}

	if got := m.nextCheckIntervalLocked(); got != unassignedCheckInterval {
		t.Fatalf("expected empty assignment mode to use %s, got %s", unassignedCheckInterval, got)
	}

	m.state.AssignmentMode = "unassigned"
	if got := m.nextCheckIntervalLocked(); got != unassignedCheckInterval {
		t.Fatalf("expected unassigned mode to use %s, got %s", unassignedCheckInterval, got)
	}

	m.state.AssignmentMode = "assigned"
	if got := m.nextCheckIntervalLocked(); got != 30*time.Second {
		t.Fatalf("expected assigned mode to use configured interval, got %s", got)
	}

	m.cfg.AuthCheckInterval = time.Second
	m.state.AssignmentMode = "unassigned"
	if got := m.nextCheckIntervalLocked(); got != time.Second {
		t.Fatalf("expected configured interval below fast poll to be preserved, got %s", got)
	}
}

func TestAssignmentEligible(t *testing.T) {
	if AssignmentEligible("assigned") != true {
		t.Fatal("expected assigned mode to be eligible")
	}
	if AssignmentEligible("managed_bundle") != true {
		t.Fatal("expected managed_bundle mode to be eligible")
	}
	if AssignmentEligible("unassigned") != false {
		t.Fatal("expected unassigned mode to be ineligible")
	}
}

func TestManagerNotifyAssignmentEligibleOnlyOnFirstEligibleTransition(t *testing.T) {
	calls := make(chan struct{}, 2)
	m := &Manager{
		cfg: Config{
			OnAssignmentEligible: func() {
				calls <- struct{}{}
			},
		},
	}

	m.state.AssignmentMode = "assigned"
	m.notifyAssignmentEligibleLocked("unassigned")

	select {
	case <-calls:
	case <-time.After(time.Second):
		t.Fatal("expected eligible transition callback")
	}

	m.notifyAssignmentEligibleLocked("assigned")

	select {
	case <-calls:
		t.Fatal("expected no callback when already eligible")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestManagerInitializeUsesPersistedAssignmentStateWhenControlPlaneUnavailable(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	server := httptest.NewServer(fake.handler())

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize online: %v", err)
	}
	server.Close()

	offlineManager, err := NewManager(Config{
		BaseURL:                      server.URL,
		NodeName:                     "node-a",
		StatePath:                    statePath,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new offline manager: %v", err)
	}
	offlineManager.now = func() time.Time { return time.Now().Add(10 * time.Minute) }

	if err := offlineManager.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize offline: %v", err)
	}
	target := offlineManager.DesiredStateTarget()
	if target.URI != fake.desiredStateURI {
		t.Fatalf("unexpected persisted desired state uri: %s", target.URI)
	}
	if got := offlineManager.DesiredStateSequenceFloor(); got != fake.desiredStateSequence {
		t.Fatalf("unexpected persisted sequence floor: %d", got)
	}
}

func TestManagerSyncKeepsPersistedAssignmentStateWhenControlPlaneUnavailable(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	server := httptest.NewServer(fake.handler())

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		GoogleMetadataEndpoint:       "",
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize online: %v", err)
	}
	server.Close()

	if err := m.Sync(context.Background()); err != nil {
		t.Fatalf("sync offline: %v", err)
	}
	target := m.DesiredStateTarget()
	if target.URI != fake.desiredStateURI {
		t.Fatalf("unexpected persisted desired state uri after sync: %s", target.URI)
	}
	if got := m.DesiredStateSequenceFloor(); got != fake.desiredStateSequence {
		t.Fatalf("unexpected sequence floor after offline sync: %d", got)
	}
}

func TestManagerGoogleAccessUsesMetadataServerBeforeControlPlaneSTS(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	metadataCalls := 0
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/instance/service-accounts/default/token" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Metadata-Flavor"); got != "Google" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		metadataCalls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "metadata-access-1",
			"expires_in":   3600,
		})
	}))
	defer metadataServer.Close()

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		GoogleMetadataEndpoint:       metadataServer.URL,
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	token, _, err := m.GoogleAccess(context.Background())
	if err != nil {
		t.Fatalf("google access: %v", err)
	}
	if token != "metadata-access-1" {
		t.Fatalf("unexpected metadata token: %q", token)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if metadataCalls != 1 {
		t.Fatalf("expected one metadata token call, got %d", metadataCalls)
	}
	if fake.subjectCalls != 0 || fake.googleSTSCalls != 0 || fake.impersonationCalls != 0 {
		t.Fatalf("expected no control-plane sts chain, got subject=%d google_sts=%d impersonation=%d", fake.subjectCalls, fake.googleSTSCalls, fake.impersonationCalls)
	}
}

func TestManagerGoogleAccessFallsBackToControlPlaneWhenMetadataUnavailable(t *testing.T) {
	fake := newFakeAuthServer(5*time.Minute, 30*time.Minute)
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	metadataCalls := 0
	metadataServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metadataCalls++
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}))
	defer metadataServer.Close()

	statePath := filepath.Join(t.TempDir(), "auth-state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{}))
	m, err := NewManager(Config{
		BaseURL:                      server.URL,
		BootstrapToken:               "bootstrap-abc",
		NodeName:                     "node-a",
		StatePath:                    statePath,
		GoogleMetadataEndpoint:       metadataServer.URL,
		GoogleSTSEndpoint:            server.URL + "/google/sts",
		GoogleIAMCredentialsEndpoint: server.URL + "/google/iam/v1",
		GoogleScopes:                 []string{"https://www.googleapis.com/auth/cloud-platform"},
	}, logger)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	if err := m.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	token, _, err := m.GoogleAccess(context.Background())
	if err != nil {
		t.Fatalf("google access: %v", err)
	}
	if !strings.HasPrefix(token, "gcp_access_") {
		t.Fatalf("unexpected fallback token: %q", token)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if metadataCalls != 1 {
		t.Fatalf("expected one metadata attempt, got %d", metadataCalls)
	}
	if fake.subjectCalls != 1 || fake.googleSTSCalls != 1 || fake.impersonationCalls != 1 {
		t.Fatalf("expected control-plane sts fallback once, got subject=%d google_sts=%d impersonation=%d", fake.subjectCalls, fake.googleSTSCalls, fake.impersonationCalls)
	}
}

func unsignedJWT(claims map[string]any) string {
	header := map[string]any{"alg": "none", "typ": "JWT"}
	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)
	return base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON) + ".sig"
}
