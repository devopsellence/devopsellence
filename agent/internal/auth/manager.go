package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/version"
)

const (
	bootstrapPath           = "/api/v1/agent/bootstrap"
	assignmentPath          = "/api/v1/agent/assignment"
	refreshPath             = "/api/v1/agent/auth/refresh"
	stsPath                 = "/api/v1/agent/sts/token"
	unassignedCheckInterval = 2 * time.Second
)

type Config struct {
	BaseURL                      string
	BootstrapToken               string
	NodeName                     string
	CloudInitInstanceDataPath    string
	StatePath                    string
	AuthCheckInterval            time.Duration
	TokenRefreshSkew             time.Duration
	GoogleIAMRetryDelays         []time.Duration
	GoogleMetadataEndpoint       string
	GoogleSTSEndpoint            string
	GoogleIAMCredentialsEndpoint string
	GoogleScopes                 []string
	HTTPClient                   *http.Client
	OnAssignmentEligible         func()
}

type State struct {
	NodeName                  string          `json:"node_name"`
	NodeID                    int64           `json:"node_id,omitempty"`
	AssignmentMode            string          `json:"assignment_mode,omitempty"`
	EnvironmentID             int64           `json:"environment_id,omitempty"`
	IdentityVersion           int64           `json:"identity_version,omitempty"`
	DesiredStateSequenceFloor int64           `json:"desired_state_sequence_floor,omitempty"`
	DesiredStateURI           string          `json:"desired_state_uri,omitempty"`
	DesiredStateInline        json.RawMessage `json:"desired_state_inline,omitempty"`
	OrganizationBundleToken   string          `json:"organization_bundle_token,omitempty"`
	EnvironmentBundleToken    string          `json:"environment_bundle_token,omitempty"`
	NodeBundleToken           string          `json:"node_bundle_token,omitempty"`
	ControlPlaneAccessToken   string          `json:"control_plane_access_token"`
	ControlPlaneAccessExpires time.Time       `json:"control_plane_access_expires_at"`
	ControlPlaneRefreshToken  string          `json:"control_plane_refresh_token"`
	GoogleAccessToken         string          `json:"google_access_token"`
	GoogleAccessExpires       time.Time       `json:"google_access_expires_at"`
	UpdatedAt                 time.Time       `json:"updated_at"`
}

type DesiredStateTarget struct {
	Mode                    string
	URI                     string
	Inline                  []byte
	OrganizationBundleToken string
	EnvironmentBundleToken  string
	NodeBundleToken         string
}

type DesiredStateSnapshot struct {
	NodeID        int64
	EnvironmentID int64
	SequenceFloor int64
	Target        DesiredStateTarget
}

type Manager struct {
	cfg        Config
	logger     *slog.Logger
	httpClient *http.Client
	now        func() time.Time

	mu    sync.RWMutex
	state State
}

func NewManager(cfg Config, logger *slog.Logger) (*Manager, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, errors.New("auth manager requires base url")
	}
	if strings.TrimSpace(cfg.NodeName) == "" {
		return nil, errors.New("auth manager requires node name")
	}
	if strings.TrimSpace(cfg.StatePath) == "" {
		return nil, errors.New("auth manager requires state path")
	}
	if cfg.AuthCheckInterval <= 0 {
		cfg.AuthCheckInterval = 30 * time.Second
	}
	if cfg.TokenRefreshSkew < 0 {
		return nil, errors.New("token refresh skew cannot be negative")
	}
	if cfg.TokenRefreshSkew == 0 {
		cfg.TokenRefreshSkew = 2 * time.Minute
	}
	if strings.TrimSpace(cfg.GoogleSTSEndpoint) == "" {
		cfg.GoogleSTSEndpoint = "https://sts.googleapis.com/v1/token"
	}
	if strings.TrimSpace(cfg.GoogleIAMCredentialsEndpoint) == "" {
		cfg.GoogleIAMCredentialsEndpoint = "https://iamcredentials.googleapis.com/v1"
	}
	if len(cfg.GoogleIAMRetryDelays) == 0 {
		cfg.GoogleIAMRetryDelays = []time.Duration{
			1 * time.Second,
			2 * time.Second,
			4 * time.Second,
			8 * time.Second,
			16 * time.Second,
			32 * time.Second,
		}
	}
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.GoogleMetadataEndpoint = strings.TrimRight(strings.TrimSpace(cfg.GoogleMetadataEndpoint), "/")
	cfg.GoogleIAMCredentialsEndpoint = strings.TrimRight(strings.TrimSpace(cfg.GoogleIAMCredentialsEndpoint), "/")
	cfg.GoogleSTSEndpoint = strings.TrimSpace(cfg.GoogleSTSEndpoint)
	cfg.NodeName = strings.TrimSpace(cfg.NodeName)

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	if logger == nil {
		logger = slog.Default()
	}

	m := &Manager{
		cfg:        cfg,
		logger:     logger,
		httpClient: httpClient,
		now:        time.Now,
	}
	return m, nil
}

func (m *Manager) Initialize(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.loadStateLocked(); err != nil {
		return err
	}
	if m.state.NodeName == "" {
		m.state.NodeName = m.cfg.NodeName
	}

	online := true

	if m.state.ControlPlaneRefreshToken == "" {
		if strings.TrimSpace(m.cfg.BootstrapToken) == "" {
			if !m.allowOfflineLocked("missing bootstrap token") {
				return fmt.Errorf("missing bootstrap token and no persisted refresh token in %s", m.cfg.StatePath)
			}
			online = false
		} else if err := m.bootstrapLocked(ctx); err != nil {
			if !m.allowOfflineLocked("bootstrap failed", err) {
				return err
			}
			online = false
		}
	}

	if online {
		if err := m.ensureControlPlaneAccessLocked(ctx); err != nil {
			if !m.allowOfflineLocked("control-plane access unavailable", err) {
				return err
			}
			online = false
		}
	}
	if online {
		if !m.managedDesiredStateLocked() {
			if err := m.fetchAssignmentLocked(ctx); err != nil {
				if !m.allowOfflineLocked("assignment fetch failed", err) {
					return err
				}
				online = false
			}
		} else {
			m.clearInlineDesiredStateLocked()
		}
	}
	if !online && m.managedDesiredStateLocked() {
		m.clearInlineDesiredStateLocked()
	}

	return m.saveStateLocked()
}

func (m *Manager) Run(ctx context.Context) error {
	timer := time.NewTimer(m.nextCheckInterval())
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			if err := m.Sync(ctx); err != nil {
				m.logger.Error("auth sync failed", "error", err)
			}
			timer.Reset(m.nextCheckInterval())
		}
	}
}

func (m *Manager) Sync(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	online := true

	if err := m.ensureControlPlaneAccessLocked(ctx); err != nil {
		if !m.allowOfflineLocked("control-plane access unavailable", err) {
			return err
		}
		online = false
	}
	if online {
		if !m.managedDesiredStateLocked() {
			if err := m.fetchAssignmentLocked(ctx); err != nil {
				if !m.allowOfflineLocked("assignment fetch failed", err) {
					return err
				}
				online = false
			}
		} else {
			m.clearInlineDesiredStateLocked()
		}
	}
	if !online && m.managedDesiredStateLocked() {
		m.clearInlineDesiredStateLocked()
	}
	if !m.googleAccessEligibleLocked() {
		m.clearGoogleTokensLocked()
	} else if strings.TrimSpace(m.state.GoogleAccessToken) != "" {
		if err := m.ensureGoogleAccessLocked(ctx); err != nil {
			if !m.allowOfflineLocked("google access refresh failed", err) {
				return err
			}
		}
	}
	return m.saveStateLocked()
}

func (m *Manager) nextCheckInterval() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.nextCheckIntervalLocked()
}

func (m *Manager) ControlPlaneAccessToken() (string, time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.ControlPlaneAccessToken, m.state.ControlPlaneAccessExpires
}

func (m *Manager) BaseURL() string {
	return m.cfg.BaseURL
}

func (m *Manager) NodeID() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.NodeID
}

func (m *Manager) EnvironmentID() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.EnvironmentID
}

func (m *Manager) GoogleAccessToken() (string, time.Time) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.GoogleAccessToken, m.state.GoogleAccessExpires
}

func (m *Manager) desiredStateTargetLocked() DesiredStateTarget {
	target := DesiredStateTarget{
		Mode:                    m.state.AssignmentMode,
		URI:                     m.state.DesiredStateURI,
		OrganizationBundleToken: m.state.OrganizationBundleToken,
		EnvironmentBundleToken:  m.state.EnvironmentBundleToken,
		NodeBundleToken:         m.state.NodeBundleToken,
	}
	if len(m.state.DesiredStateInline) > 0 {
		target.Inline = append([]byte(nil), m.state.DesiredStateInline...)
	}
	return target
}

func (m *Manager) desiredStateSnapshotMatchesLocked(snapshot DesiredStateSnapshot) bool {
	if snapshot.NodeID != m.state.NodeID || snapshot.EnvironmentID != m.state.EnvironmentID {
		return false
	}
	return desiredStateTargetsEqual(snapshot.Target, m.desiredStateTargetLocked())
}

func desiredStateTargetsEqual(left, right DesiredStateTarget) bool {
	return left.Mode == right.Mode &&
		left.URI == right.URI &&
		left.OrganizationBundleToken == right.OrganizationBundleToken &&
		left.EnvironmentBundleToken == right.EnvironmentBundleToken &&
		left.NodeBundleToken == right.NodeBundleToken &&
		bytes.Equal(left.Inline, right.Inline)
}

func (m *Manager) canOperateOfflineLocked() bool {
	target := m.desiredStateTargetLocked()
	return strings.TrimSpace(target.URI) != "" || len(target.Inline) > 0
}

func (m *Manager) allowOfflineLocked(reason string, err ...error) bool {
	if !m.canOperateOfflineLocked() {
		return false
	}
	if len(err) > 0 && err[0] != nil {
		m.logger.Warn("using persisted assignment state offline", "reason", reason, "error", err[0])
	} else {
		m.logger.Warn("using persisted assignment state offline", "reason", reason)
	}
	return true
}

func (m *Manager) DesiredStateTarget() DesiredStateTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.desiredStateTargetLocked()
}

func (m *Manager) DesiredStateSnapshot() DesiredStateSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return DesiredStateSnapshot{
		NodeID:        m.state.NodeID,
		EnvironmentID: m.state.EnvironmentID,
		SequenceFloor: m.state.DesiredStateSequenceFloor,
		Target:        m.desiredStateTargetLocked(),
	}
}

func (m *Manager) DesiredStateSequenceFloor() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state.DesiredStateSequenceFloor
}

func (m *Manager) RecordDesiredStateSequenceFloor(sequence int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if sequence <= m.state.DesiredStateSequenceFloor {
		return nil
	}
	m.state.DesiredStateSequenceFloor = sequence
	return m.saveStateLocked()
}

func (m *Manager) RecordDesiredStateSequenceFloorForSnapshot(snapshot DesiredStateSnapshot, sequence int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.desiredStateSnapshotMatchesLocked(snapshot) {
		return nil
	}
	if sequence <= m.state.DesiredStateSequenceFloor {
		return nil
	}
	m.state.DesiredStateSequenceFloor = sequence
	return m.saveStateLocked()
}

func (m *Manager) GoogleAccess(ctx context.Context) (string, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureGoogleAccessLocked(ctx); err != nil {
		return "", time.Time{}, err
	}
	if err := m.saveStateLocked(); err != nil {
		return "", time.Time{}, err
	}
	return m.state.GoogleAccessToken, m.state.GoogleAccessExpires, nil
}

func (m *Manager) ControlPlaneAccess(ctx context.Context) (string, time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ensureControlPlaneAccessLocked(ctx); err != nil {
		return "", time.Time{}, err
	}
	if err := m.saveStateLocked(); err != nil {
		return "", time.Time{}, err
	}
	return m.state.ControlPlaneAccessToken, m.state.ControlPlaneAccessExpires, nil
}

func (m *Manager) InvalidateGoogleAccess() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearGoogleTokensLocked()
	return m.saveStateLocked()
}

func (m *Manager) ensureControlPlaneAccessLocked(ctx context.Context) error {
	now := m.now()
	if tokenValid(now, m.state.ControlPlaneAccessToken, m.state.ControlPlaneAccessExpires, m.cfg.TokenRefreshSkew) {
		return nil
	}

	if m.state.ControlPlaneRefreshToken == "" {
		return m.bootstrapLocked(ctx)
	}

	if err := m.refreshControlPlaneLocked(ctx); err != nil {
		if isStatusCode(err, http.StatusUnauthorized) && strings.TrimSpace(m.cfg.BootstrapToken) != "" {
			m.logger.Warn("control-plane refresh unauthorized, retrying bootstrap")
			return m.bootstrapLocked(ctx)
		}
		return err
	}
	return nil
}

func (m *Manager) ensureGoogleAccessLocked(ctx context.Context) error {
	if !m.googleAccessEligibleLocked() {
		return errors.New("google access unavailable while node is unassigned")
	}

	now := m.now()
	if tokenValid(now, m.state.GoogleAccessToken, m.state.GoogleAccessExpires, m.cfg.TokenRefreshSkew) {
		return nil
	}

	if accessToken, accessExpiry, err := m.googleAccessFromMetadataLocked(ctx); err == nil {
		m.state.GoogleAccessToken = accessToken
		m.state.GoogleAccessExpires = accessExpiry
		return nil
	}

	if err := m.ensureControlPlaneAccessLocked(ctx); err != nil {
		return err
	}

	subject, audience, err := m.fetchSubjectTokenLocked(ctx)
	if err != nil {
		return err
	}
	serviceAccount, err := extractServiceAccountEmail(subject)
	if err != nil {
		return err
	}

	federatedToken, _, err := m.exchangeWithGoogleSTSLocked(ctx, subject, audience)
	if err != nil {
		return err
	}

	accessToken, accessExpiry, err := m.generateServiceAccountAccessTokenLocked(ctx, serviceAccount, federatedToken)
	if err != nil {
		return err
	}

	m.state.GoogleAccessToken = accessToken
	m.state.GoogleAccessExpires = accessExpiry
	return nil
}

func (m *Manager) googleAccessFromMetadataLocked(ctx context.Context) (string, time.Time, error) {
	if m.cfg.GoogleMetadataEndpoint == "" {
		return "", time.Time{}, errors.New("google metadata endpoint disabled")
	}

	metadataCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	var response struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	endpoint := m.cfg.GoogleMetadataEndpoint + "/instance/service-accounts/default/token"
	req, err := http.NewRequestWithContext(metadataCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("build metadata token request: %w", err)
	}
	req.Header.Set("Metadata-Flavor", "Google")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("metadata token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return "", time.Time{}, &httpStatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", time.Time{}, fmt.Errorf("decode metadata token response: %w", err)
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return "", time.Time{}, errors.New("metadata token response missing access_token")
	}
	if response.ExpiresIn <= 0 {
		return "", time.Time{}, errors.New("metadata token response missing expires_in")
	}
	return response.AccessToken, m.now().Add(time.Duration(response.ExpiresIn) * time.Second), nil
}

func (m *Manager) bootstrapLocked(ctx context.Context) error {
	providerServerID := m.cloudInitInstanceIDLocked()
	var response bootstrapResponse
	if err := m.doJSON(ctx, http.MethodPost, m.cfg.BaseURL+bootstrapPath, "", bootstrapRequest{
		BootstrapToken:   m.cfg.BootstrapToken,
		Name:             m.cfg.NodeName,
		ProviderServerID: providerServerID,
	}, &response); err != nil {
		return fmt.Errorf("bootstrap request failed: %w", err)
	}

	m.state.NodeName = m.cfg.NodeName
	m.state.NodeID = response.NodeID
	m.state.ControlPlaneAccessToken = response.AccessToken
	m.state.ControlPlaneRefreshToken = response.RefreshToken
	m.state.ControlPlaneAccessExpires = m.now().Add(time.Duration(response.ExpiresIn) * time.Second)
	m.applyDesiredStateTargetLocked(response.DesiredStateTarget)
	m.logger.Info("bootstrap succeeded")
	return nil
}

func (m *Manager) cloudInitInstanceIDLocked() string {
	path := strings.TrimSpace(m.cfg.CloudInitInstanceDataPath)
	if path == "" {
		return ""
	}

	data, err := os.ReadFile(path)
	if err != nil {
		m.logger.Debug("cloud-init instance data unavailable", "path", path, "error", err)
		return ""
	}

	var payload struct {
		V1 struct {
			InstanceID string `json:"instance_id"`
		} `json:"v1"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		m.logger.Warn("parse cloud-init instance data failed", "path", path, "error", err)
		return ""
	}

	instanceID := strings.TrimSpace(payload.V1.InstanceID)
	if instanceID == "" {
		m.logger.Debug("cloud-init instance data missing instance id", "path", path)
		return ""
	}
	return instanceID
}

func (m *Manager) refreshControlPlaneLocked(ctx context.Context) error {
	var response refreshResponse
	if err := m.doJSON(ctx, http.MethodPost, m.cfg.BaseURL+refreshPath, "", refreshRequest{
		RefreshToken: m.state.ControlPlaneRefreshToken,
	}, &response); err != nil {
		return fmt.Errorf("control-plane refresh failed: %w", err)
	}

	m.state.ControlPlaneAccessToken = response.AccessToken
	if strings.TrimSpace(response.RefreshToken) != "" {
		m.state.ControlPlaneRefreshToken = response.RefreshToken
	}
	m.state.ControlPlaneAccessExpires = m.now().Add(time.Duration(response.ExpiresIn) * time.Second)
	m.applyDesiredStateTargetLocked(response.DesiredStateTarget)
	m.logger.Debug("control-plane access token refreshed", "expires_at", m.state.ControlPlaneAccessExpires)
	return nil
}

func (m *Manager) fetchAssignmentLocked(ctx context.Context) error {
	var response assignmentResponse
	if err := m.doJSON(ctx, http.MethodGet, m.cfg.BaseURL+assignmentPath, m.state.ControlPlaneAccessToken, nil, &response); err != nil {
		return fmt.Errorf("assignment request failed: %w", err)
	}

	previousMode := m.state.AssignmentMode
	previousEnvironmentID := m.state.EnvironmentID
	previousIdentityVersion := m.state.IdentityVersion
	previousDesiredStateSequenceFloor := m.state.DesiredStateSequenceFloor
	previousDesiredStateURI := m.state.DesiredStateURI
	previousInline := string(m.state.DesiredStateInline)
	previousOrgBundleToken := m.state.OrganizationBundleToken
	previousEnvBundleToken := m.state.EnvironmentBundleToken
	previousNodeBundleToken := m.state.NodeBundleToken

	switch strings.TrimSpace(response.Mode) {
	case "assigned":
		if strings.TrimSpace(response.DesiredStateURI) == "" {
			return errors.New("assignment response missing desired_state_uri")
		}
		m.state.AssignmentMode = "assigned"
		m.state.EnvironmentID = response.EnvironmentID
		m.state.IdentityVersion = response.IdentityVersion
		orgBundleToken := strings.TrimSpace(response.OrganizationBundleToken)
		envBundleToken := strings.TrimSpace(response.EnvironmentBundleToken)
		nodeBundleToken := strings.TrimSpace(response.NodeBundleToken)
		if previousMode != "assigned" || previousEnvironmentID != response.EnvironmentID || previousIdentityVersion != response.IdentityVersion ||
			previousOrgBundleToken != orgBundleToken || previousEnvBundleToken != envBundleToken || previousNodeBundleToken != nodeBundleToken {
			m.state.DesiredStateSequenceFloor = response.DesiredStateSequence
		} else if response.DesiredStateSequence > m.state.DesiredStateSequenceFloor {
			m.state.DesiredStateSequenceFloor = response.DesiredStateSequence
		}
		m.state.DesiredStateURI = strings.TrimSpace(response.DesiredStateURI)
		m.state.DesiredStateInline = nil
		m.state.OrganizationBundleToken = orgBundleToken
		m.state.EnvironmentBundleToken = envBundleToken
		m.state.NodeBundleToken = nodeBundleToken
	case "unassigned":
		inline := response.DesiredState
		if len(inline) == 0 {
			inline = json.RawMessage(`{"schemaVersion":2,"revision":"unassigned","environments":[]}`)
		}
		m.state.AssignmentMode = "unassigned"
		m.state.EnvironmentID = 0
		m.state.IdentityVersion = 0
		if previousMode != "unassigned" ||
			response.DesiredStateSequence < m.state.DesiredStateSequenceFloor ||
			previousInline != string(inline) {
			m.state.DesiredStateSequenceFloor = response.DesiredStateSequence
		} else if response.DesiredStateSequence > m.state.DesiredStateSequenceFloor {
			m.state.DesiredStateSequenceFloor = response.DesiredStateSequence
		}
		m.state.DesiredStateURI = ""
		m.state.DesiredStateInline = append(m.state.DesiredStateInline[:0], inline...)
		m.state.OrganizationBundleToken = ""
		m.state.EnvironmentBundleToken = ""
		m.state.NodeBundleToken = ""
	default:
		return fmt.Errorf("unsupported assignment mode %q", response.Mode)
	}

	assignmentChanged := previousMode != m.state.AssignmentMode ||
		previousEnvironmentID != m.state.EnvironmentID ||
		previousIdentityVersion != m.state.IdentityVersion ||
		previousDesiredStateSequenceFloor != m.state.DesiredStateSequenceFloor ||
		previousDesiredStateURI != m.state.DesiredStateURI ||
		previousInline != string(m.state.DesiredStateInline) ||
		previousOrgBundleToken != m.state.OrganizationBundleToken ||
		previousEnvBundleToken != m.state.EnvironmentBundleToken ||
		previousNodeBundleToken != m.state.NodeBundleToken
	if assignmentChanged {
		m.clearGoogleTokensLocked()
		m.logger.Info("assignment updated", "mode", m.state.AssignmentMode, "environment_id", m.state.EnvironmentID, "node_bundle_token", m.state.NodeBundleToken)
		m.notifyAssignmentEligibleLocked(previousMode)
	}
	return nil
}

func (m *Manager) fetchSubjectTokenLocked(ctx context.Context) (string, string, error) {
	var response subjectTokenResponse
	if err := m.doJSON(ctx, http.MethodPost, m.cfg.BaseURL+stsPath, m.state.ControlPlaneAccessToken, subjectTokenRequest{}, &response); err != nil {
		return "", "", fmt.Errorf("subject token request failed: %w", err)
	}
	if strings.TrimSpace(response.SubjectToken) == "" {
		return "", "", errors.New("subject token response missing subject_token")
	}
	if strings.TrimSpace(response.Audience) == "" {
		return "", "", errors.New("subject token response missing audience")
	}
	return response.SubjectToken, response.Audience, nil
}

func (m *Manager) exchangeWithGoogleSTSLocked(ctx context.Context, subjectToken, audience string) (string, time.Time, error) {
	requestBody := googleSTSRequest{
		GrantType:          "urn:ietf:params:oauth:grant-type:token-exchange",
		RequestedTokenType: "urn:ietf:params:oauth:token-type:access_token",
		SubjectTokenType:   "urn:ietf:params:oauth:token-type:jwt",
		SubjectToken:       subjectToken,
		Audience:           audience,
		Scope:              strings.Join(m.cfg.GoogleScopes, " "),
	}

	var response googleSTSResponse
	if err := m.doJSON(ctx, http.MethodPost, m.cfg.GoogleSTSEndpoint, "", requestBody, &response); err != nil {
		return "", time.Time{}, fmt.Errorf("google sts exchange failed: %w", err)
	}
	if strings.TrimSpace(response.AccessToken) == "" {
		return "", time.Time{}, errors.New("google sts response missing access_token")
	}
	expiresAt := m.now().Add(time.Duration(response.ExpiresIn) * time.Second)
	return response.AccessToken, expiresAt, nil
}

func (m *Manager) generateServiceAccountAccessTokenLocked(ctx context.Context, serviceAccountEmail, federatedToken string) (string, time.Time, error) {
	if strings.TrimSpace(serviceAccountEmail) == "" {
		return "", time.Time{}, errors.New("service account email is required")
	}
	if strings.TrimSpace(federatedToken) == "" {
		return "", time.Time{}, errors.New("federated token is required")
	}

	escaped := url.PathEscape(serviceAccountEmail)
	endpoint := fmt.Sprintf("%s/projects/-/serviceAccounts/%s:generateAccessToken", m.cfg.GoogleIAMCredentialsEndpoint, escaped)

	request := impersonationRequest{Scope: m.cfg.GoogleScopes}
	attempts := len(m.cfg.GoogleIAMRetryDelays) + 1

	for attempt := 1; attempt <= attempts; attempt++ {
		var response impersonationResponse
		err := m.doJSON(ctx, http.MethodPost, endpoint, federatedToken, request, &response)
		if err == nil {
			if strings.TrimSpace(response.AccessToken) == "" {
				return "", time.Time{}, errors.New("iamcredentials response missing accessToken")
			}
			expiresAt, parseErr := time.Parse(time.RFC3339, response.ExpireTime)
			if parseErr != nil {
				return "", time.Time{}, fmt.Errorf("invalid expireTime: %w", parseErr)
			}
			return response.AccessToken, expiresAt, nil
		}

		if !retryableIAMCredentialsError(err) || attempt == attempts {
			return "", time.Time{}, fmt.Errorf("iamcredentials generateAccessToken failed: %w", err)
		}

		delay := m.cfg.GoogleIAMRetryDelays[attempt-1]
		m.logger.Warn("iamcredentials generateAccessToken not ready yet", "attempt", attempt, "delay", delay, "error", err)
		if waitErr := sleepContext(ctx, delay); waitErr != nil {
			return "", time.Time{}, fmt.Errorf("iamcredentials generateAccessToken failed: %w", waitErr)
		}
	}

	return "", time.Time{}, errors.New("iamcredentials generateAccessToken failed: exhausted retries")
}

func (m *Manager) loadStateLocked() error {
	data, err := os.ReadFile(m.cfg.StatePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read auth state: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse auth state: %w", err)
	}
	m.state = state
	return nil
}

func (m *Manager) clearGoogleTokensLocked() {
	m.state.GoogleAccessToken = ""
	m.state.GoogleAccessExpires = time.Time{}
}

func (m *Manager) clearInlineDesiredStateLocked() {
	m.state.DesiredStateInline = nil
}

func (m *Manager) managedDesiredStateLocked() bool {
	return strings.TrimSpace(m.state.AssignmentMode) == "managed_bundle" && strings.TrimSpace(m.state.DesiredStateURI) != ""
}

func (m *Manager) nextCheckIntervalLocked() time.Duration {
	interval := m.cfg.AuthCheckInterval
	mode := strings.TrimSpace(m.state.AssignmentMode)

	if mode == "" || mode == "unassigned" {
		if interval > unassignedCheckInterval {
			return unassignedCheckInterval
		}
	}

	return interval
}

func (m *Manager) googleAccessEligibleLocked() bool {
	switch strings.TrimSpace(m.state.AssignmentMode) {
	case "assigned", "managed_bundle":
		return true
	default:
		return false
	}
}

func (m *Manager) applyDesiredStateTargetLocked(target *desiredStateTargetResponse) {
	if target == nil || strings.TrimSpace(target.DesiredStateURI) == "" {
		if m.managedDesiredStateLocked() {
			m.state.AssignmentMode = ""
			m.state.DesiredStateURI = ""
			m.state.OrganizationBundleToken = ""
			m.state.EnvironmentBundleToken = ""
			m.state.NodeBundleToken = ""
			m.clearInlineDesiredStateLocked()
			m.clearGoogleTokensLocked()
		}
		return
	}

	mode := strings.TrimSpace(target.Mode)
	if mode == "" {
		mode = "managed_bundle"
	}
	previousMode := m.state.AssignmentMode
	previousURI := m.state.DesiredStateURI
	previousOrgBundleToken := m.state.OrganizationBundleToken
	previousEnvBundleToken := m.state.EnvironmentBundleToken
	previousNodeBundleToken := m.state.NodeBundleToken
	previousSequenceFloor := m.state.DesiredStateSequenceFloor

	m.state.AssignmentMode = mode
	m.state.EnvironmentID = 0
	m.state.IdentityVersion = 0
	m.state.DesiredStateURI = strings.TrimSpace(target.DesiredStateURI)
	m.state.OrganizationBundleToken = strings.TrimSpace(target.OrganizationBundleToken)
	m.state.EnvironmentBundleToken = strings.TrimSpace(target.EnvironmentBundleToken)
	m.state.NodeBundleToken = strings.TrimSpace(target.NodeBundleToken)
	m.clearInlineDesiredStateLocked()

	if previousMode != mode ||
		previousURI != m.state.DesiredStateURI ||
		previousOrgBundleToken != m.state.OrganizationBundleToken ||
		previousEnvBundleToken != m.state.EnvironmentBundleToken ||
		previousNodeBundleToken != m.state.NodeBundleToken {
		m.state.DesiredStateSequenceFloor = target.DesiredStateSequence
	} else if target.DesiredStateSequence > m.state.DesiredStateSequenceFloor {
		m.state.DesiredStateSequenceFloor = target.DesiredStateSequence
	}

	if previousMode != m.state.AssignmentMode ||
		previousURI != m.state.DesiredStateURI ||
		previousOrgBundleToken != m.state.OrganizationBundleToken ||
		previousEnvBundleToken != m.state.EnvironmentBundleToken ||
		previousNodeBundleToken != m.state.NodeBundleToken ||
		previousSequenceFloor != m.state.DesiredStateSequenceFloor {
		m.clearGoogleTokensLocked()
		m.logger.Info("desired state target updated", "mode", m.state.AssignmentMode, "node_bundle_token", m.state.NodeBundleToken)
		m.notifyAssignmentEligibleLocked(previousMode)
	}
}

func (m *Manager) notifyAssignmentEligibleLocked(previousMode string) {
	if m.cfg.OnAssignmentEligible == nil {
		return
	}
	if AssignmentEligible(previousMode) || !AssignmentEligible(m.state.AssignmentMode) {
		return
	}
	go m.cfg.OnAssignmentEligible()
}

func AssignmentEligible(mode string) bool {
	switch strings.TrimSpace(mode) {
	case "assigned", "managed_bundle":
		return true
	default:
		return false
	}
}

func (m *Manager) saveStateLocked() error {
	m.state.UpdatedAt = m.now().UTC()

	dir := filepath.Dir(m.cfg.StatePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create auth state dir: %w", err)
	}

	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode auth state: %w", err)
	}

	tmp := m.cfg.StatePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write auth state temp file: %w", err)
	}
	if err := os.Rename(tmp, m.cfg.StatePath); err != nil {
		return fmt.Errorf("replace auth state file: %w", err)
	}
	return nil
}

func (m *Manager) doJSON(ctx context.Context, method, targetURL, bearerToken string, requestBody any, responseBody any) error {
	var payload []byte
	var err error
	if requestBody != nil {
		payload, err = json.Marshal(requestBody)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
	}

	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	req.Header.Set(version.CapabilitiesHeader, version.CapabilityHeaderValue())
	if requestBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(bearerToken) != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return &httpStatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}

	if responseBody == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(responseBody); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}

func extractServiceAccountEmail(subjectToken string) (string, error) {
	parts := strings.Split(subjectToken, ".")
	if len(parts) != 3 {
		return "", errors.New("subject token is not a jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode jwt payload: %w", err)
	}
	var claims struct {
		ServiceAccountEmail string `json:"service_account_email"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parse jwt payload: %w", err)
	}
	if strings.TrimSpace(claims.ServiceAccountEmail) == "" {
		return "", errors.New("subject token missing service_account_email claim")
	}
	return claims.ServiceAccountEmail, nil
}

func tokenValid(now time.Time, token string, expiresAt time.Time, skew time.Duration) bool {
	if strings.TrimSpace(token) == "" {
		return false
	}
	if expiresAt.IsZero() {
		return false
	}
	return now.Before(expiresAt.Add(-skew))
}

func retryableIAMCredentialsError(err error) bool {
	var httpErr *httpStatusError
	if !errors.As(err, &httpErr) {
		return false
	}
	if httpErr.StatusCode >= 500 {
		return true
	}

	body := strings.ToLower(httpErr.Body)
	if httpErr.StatusCode == http.StatusForbidden {
		return strings.Contains(body, "iam_permission_denied") ||
			strings.Contains(body, "iam.serviceaccounts.getaccesstoken") ||
			strings.Contains(body, "permission_denied")
	}
	if httpErr.StatusCode == http.StatusNotFound {
		return strings.Contains(body, "gaia id not found") || strings.Contains(body, "not found")
	}
	return false
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type httpStatusError struct {
	StatusCode int
	Body       string
}

func (e *httpStatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("http status %d", e.StatusCode)
	}
	return fmt.Sprintf("http status %d: %s", e.StatusCode, e.Body)
}

func isStatusCode(err error, statusCode int) bool {
	var httpErr *httpStatusError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == statusCode
}

type bootstrapRequest struct {
	BootstrapToken   string `json:"bootstrap_token"`
	Name             string `json:"name,omitempty"`
	ProviderServerID string `json:"provider_server_id,omitempty"`
}

type bootstrapResponse struct {
	NodeID             int64                       `json:"node_id"`
	AccessToken        string                      `json:"access_token"`
	RefreshToken       string                      `json:"refresh_token"`
	ExpiresIn          int64                       `json:"expires_in"`
	DesiredStateTarget *desiredStateTargetResponse `json:"desired_state_target"`
}

type assignmentResponse struct {
	Mode                    string          `json:"mode"`
	EnvironmentID           int64           `json:"environment_id"`
	IdentityVersion         int64           `json:"identity_version"`
	DesiredStateSequence    int64           `json:"desired_state_sequence"`
	DesiredStateURI         string          `json:"desired_state_uri"`
	DesiredState            json.RawMessage `json:"desired_state"`
	OrganizationBundleToken string          `json:"organization_bundle_token"`
	EnvironmentBundleToken  string          `json:"environment_bundle_token"`
	NodeBundleToken         string          `json:"node_bundle_token"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type refreshResponse struct {
	AccessToken        string                      `json:"access_token"`
	RefreshToken       string                      `json:"refresh_token"`
	ExpiresIn          int64                       `json:"expires_in"`
	DesiredStateTarget *desiredStateTargetResponse `json:"desired_state_target"`
}

type desiredStateTargetResponse struct {
	Mode                    string `json:"mode"`
	DesiredStateSequence    int64  `json:"desired_state_sequence"`
	DesiredStateURI         string `json:"desired_state_uri"`
	OrganizationBundleToken string `json:"organization_bundle_token"`
	EnvironmentBundleToken  string `json:"environment_bundle_token"`
	NodeBundleToken         string `json:"node_bundle_token"`
}

type subjectTokenRequest struct {
}

type subjectTokenResponse struct {
	SubjectToken string `json:"subject_token"`
	Audience     string `json:"audience"`
}

type googleSTSRequest struct {
	GrantType          string `json:"grantType"`
	RequestedTokenType string `json:"requestedTokenType"`
	SubjectTokenType   string `json:"subjectTokenType"`
	SubjectToken       string `json:"subjectToken"`
	Audience           string `json:"audience"`
	Scope              string `json:"scope,omitempty"`
}

type googleSTSResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

type impersonationRequest struct {
	Scope []string `json:"scope"`
}

type impersonationResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireTime  string `json:"expireTime"`
}
