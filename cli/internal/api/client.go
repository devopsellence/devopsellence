package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/devopsellence/cli/internal/version"
)

const DefaultBaseURL = "https://www.devopsellence.com"

type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

type StatusError struct {
	StatusCode int
	Message    string
}

func (e *StatusError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return fmt.Sprintf("API request failed with status %d", e.StatusCode)
}

type Organization struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Role     string `json:"role,omitempty"`
	PlanTier string `json:"plan_tier,omitempty"`
}

type Project struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Environment struct {
	ID              int    `json:"id"`
	Name            string `json:"name"`
	IngressStrategy string `json:"ingress_strategy,omitempty"`
}

type DeployTargetResponse struct {
	Organization        Organization `json:"organization"`
	OrganizationCreated bool         `json:"organization_created"`
	Project             Project      `json:"project"`
	ProjectCreated      bool         `json:"project_created"`
	Environment         Environment  `json:"environment"`
	EnvironmentCreated  bool         `json:"environment_created"`
}

type EnvironmentSecret struct {
	ID            int    `json:"id"`
	EnvironmentID int    `json:"environment_id"`
	ServiceName   string `json:"service_name"`
	Name          string `json:"name"`
	GCPSecretName string `json:"gcp_secret_name"`
	SecretRef     string `json:"secret_ref"`
	ValueSHA256   string `json:"value_sha256"`
	UpdatedAt     string `json:"updated_at"`
}

type Node struct {
	ID             int            `json:"id"`
	Name           string         `json:"name"`
	OrganizationID int            `json:"organization_id,omitempty"`
	Labels         []string       `json:"labels"`
	Environment    map[string]any `json:"environment,omitempty"`
	RevokedAt      string         `json:"revoked_at,omitempty"`
}

type NodeDiagnoseSummary struct {
	Status       string `json:"status"`
	Total        int    `json:"total"`
	Running      int    `json:"running"`
	Stopped      int    `json:"stopped"`
	Unhealthy    int    `json:"unhealthy"`
	LogsIncluded int    `json:"logs_included"`
}

type NodeDiagnoseContainer struct {
	Name            string            `json:"name"`
	Service         string            `json:"service,omitempty"`
	System          string            `json:"system,omitempty"`
	Image           string            `json:"image"`
	Hash            string            `json:"hash,omitempty"`
	Running         bool              `json:"running"`
	Health          string            `json:"health,omitempty"`
	HasHealthcheck  bool              `json:"has_healthcheck"`
	PublishHostPort bool              `json:"publish_host_port"`
	NetworkIPs      map[string]string `json:"network_ips,omitempty"`
	LogTail         string            `json:"log_tail,omitempty"`
}

type NodeDiagnoseResult struct {
	CollectedAt  string                  `json:"collected_at"`
	AgentVersion string                  `json:"agent_version"`
	Summary      NodeDiagnoseSummary     `json:"summary"`
	Containers   []NodeDiagnoseContainer `json:"containers"`
}

type NodeDiagnoseRequest struct {
	ID           int                 `json:"id"`
	Status       string              `json:"status"`
	RequestedAt  string              `json:"requested_at"`
	ClaimedAt    string              `json:"claimed_at"`
	CompletedAt  string              `json:"completed_at"`
	ErrorMessage string              `json:"error_message"`
	Node         Node                `json:"node"`
	Result       *NodeDiagnoseResult `json:"result"`
}

type GARPushAuth struct {
	RegistryHost      string `json:"registry_host"`
	GARRepositoryPath string `json:"gar_repository_path"`
	RepositoryPath    string `json:"repository_path"`
	ImageRepository   string `json:"image_repository"`
	DockerUsername    string `json:"docker_username"`
	DockerPassword    string `json:"docker_password"`
	ExpiresIn         int64  `json:"expires_in"`
	AccessToken       string `json:"access_token"`
}

type OrganizationRegistryConfig struct {
	Configured          bool   `json:"configured"`
	OrganizationID      int    `json:"organization_id,omitempty"`
	RegistryHost        string `json:"registry_host,omitempty"`
	RepositoryNamespace string `json:"repository_namespace,omitempty"`
	Username            string `json:"username,omitempty"`
	ExpiresAt           string `json:"expires_at,omitempty"`
}

type DeploymentProgressSummary struct {
	AssignedNodes int  `json:"assigned_nodes"`
	Pending       int  `json:"pending"`
	Reconciling   int  `json:"reconciling"`
	Settled       int  `json:"settled"`
	Error         int  `json:"error"`
	Active        bool `json:"active"`
	Complete      bool `json:"complete"`
	Failed        bool `json:"failed"`
}

type DeploymentProgressNode struct {
	ID         int              `json:"id"`
	Name       string           `json:"name"`
	Labels     []string         `json:"labels"`
	Phase      string           `json:"phase"`
	Message    string           `json:"message"`
	Error      string           `json:"error"`
	ReportedAt string           `json:"reported_at"`
	Containers []map[string]any `json:"containers"`
}

type DeploymentProgressIngress struct {
	Hostname  string `json:"hostname"`
	PublicURL string `json:"public_url"`
	Status    string `json:"status"`
}

type DeploymentProgress struct {
	ID            int                        `json:"id"`
	Sequence      int                        `json:"sequence"`
	Status        string                     `json:"status"`
	StatusMessage string                     `json:"status_message"`
	ErrorMessage  string                     `json:"error_message"`
	PublishedAt   string                     `json:"published_at"`
	FinishedAt    string                     `json:"finished_at"`
	Environment   Environment                `json:"environment"`
	Release       map[string]any             `json:"release"`
	Summary       DeploymentProgressSummary  `json:"summary"`
	Nodes         []DeploymentProgressNode   `json:"nodes"`
	Ingress       *DeploymentProgressIngress `json:"ingress"`
}

type ApiToken struct {
	ID         int    `json:"id,omitempty"`
	Token      string `json:"token,omitempty"`
	Name       string `json:"name"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at,omitempty"`
	RevokedAt  string `json:"revoked_at,omitempty"`
	Current    bool   `json:"current,omitempty"`
}

type ReleaseCreateRequest struct {
	GitSHA          string         `json:"git_sha"`
	ImageRepository string         `json:"image_repository"`
	ImageDigest     string         `json:"image_digest"`
	Web             map[string]any `json:"web,omitempty"`
	Worker          map[string]any `json:"worker,omitempty"`
	ReleaseCommand  string         `json:"release_command,omitempty"`
}

func New(baseURL string) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: http.DefaultClient,
	}
}

func (c *Client) ListOrganizations(ctx context.Context, token string) ([]Organization, error) {
	var body struct {
		Organizations []Organization `json:"organizations"`
	}
	if err := c.get(ctx, token, "/api/v1/cli/organizations", nil, &body); err != nil {
		return nil, err
	}
	return body.Organizations, nil
}

func (c *Client) CreateOrganization(ctx context.Context, token, name string) (Organization, error) {
	var body Organization
	err := c.post(ctx, token, "/api/v1/cli/organizations", map[string]any{"name": name}, &body)
	return body, err
}

func (c *Client) ListProjects(ctx context.Context, token string, organizationID int) ([]Project, error) {
	var body struct {
		Projects []Project `json:"projects"`
	}
	err := c.get(ctx, token, "/api/v1/cli/projects", map[string]string{"organization_id": fmt.Sprintf("%d", organizationID)}, &body)
	return body.Projects, err
}

func (c *Client) CreateProject(ctx context.Context, token string, organizationID int, name string) (Project, error) {
	var body Project
	err := c.post(ctx, token, "/api/v1/cli/projects", map[string]any{"organization_id": organizationID, "name": name}, &body)
	return body, err
}

func (c *Client) DeleteProject(ctx context.Context, token string, projectID int) (map[string]any, error) {
	var body map[string]any
	err := c.request(ctx, http.MethodDelete, token, fmt.Sprintf("/api/v1/cli/projects/%d", projectID), nil, nil, &body)
	return body, err
}

func (c *Client) ListEnvironments(ctx context.Context, token string, projectID int) ([]Environment, error) {
	var body struct {
		Environments []Environment `json:"environments"`
	}
	err := c.get(ctx, token, fmt.Sprintf("/api/v1/cli/projects/%d/environments", projectID), nil, &body)
	return body.Environments, err
}

func (c *Client) CreateEnvironment(ctx context.Context, token string, projectID int, name string, ingressStrategy string) (Environment, error) {
	var body Environment
	payload := map[string]any{"name": name}
	if strings.TrimSpace(ingressStrategy) != "" {
		payload["ingress_strategy"] = strings.TrimSpace(ingressStrategy)
	}
	err := c.post(ctx, token, fmt.Sprintf("/api/v1/cli/projects/%d/environments", projectID), payload, &body)
	return body, err
}

func (c *Client) UpdateEnvironmentIngressStrategy(ctx context.Context, token string, environmentID int, ingressStrategy string) (Environment, error) {
	var body Environment
	err := c.request(ctx, http.MethodPatch, token, fmt.Sprintf("/api/v1/cli/environments/%d/ingress", environmentID), nil, map[string]any{
		"ingress_strategy": strings.TrimSpace(ingressStrategy),
	}, &body)
	return body, err
}

func (c *Client) ResolveDeployTarget(ctx context.Context, token, organization, project, environment string, preferredOrganizationID int) (DeployTargetResponse, error) {
	payload := map[string]any{
		"organization": organization,
		"project":      project,
		"environment":  environment,
	}
	if preferredOrganizationID > 0 {
		payload["preferred_organization_id"] = preferredOrganizationID
	}
	var body DeployTargetResponse
	err := c.post(ctx, token, "/api/v1/cli/deploy_target", payload, &body)
	if shouldFallbackDeployTarget(err) {
		return c.resolveDeployTargetLegacy(ctx, token, organization, project, environment, preferredOrganizationID)
	}
	return body, err
}

func shouldFallbackDeployTarget(err error) bool {
	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.StatusCode == http.StatusNotFound || statusErr.StatusCode == http.StatusMethodNotAllowed
}

func (c *Client) resolveDeployTargetLegacy(ctx context.Context, token, organizationInput, projectName, environmentName string, preferredOrganizationID int) (DeployTargetResponse, error) {
	orgs, err := c.ListOrganizations(ctx, token)
	if err != nil {
		return DeployTargetResponse{}, err
	}

	org, orgCreated, err := c.resolveLegacyOrganization(ctx, token, orgs, organizationInput, preferredOrganizationID)
	if err != nil {
		return DeployTargetResponse{}, err
	}

	projects, err := c.ListProjects(ctx, token, org.ID)
	if err != nil {
		return DeployTargetResponse{}, err
	}
	project, projectCreated := findProjectByName(projects, projectName)
	if !projectCreated {
		project, err = c.CreateProject(ctx, token, org.ID, projectName)
		if err != nil {
			return DeployTargetResponse{}, err
		}
	}

	environments, err := c.ListEnvironments(ctx, token, project.ID)
	if err != nil {
		return DeployTargetResponse{}, err
	}
	environment, environmentCreated := findEnvironmentByName(environments, environmentName)
	if !environmentCreated {
		environment, err = c.CreateEnvironment(ctx, token, project.ID, environmentName, "")
		if err != nil {
			return DeployTargetResponse{}, err
		}
	}

	return DeployTargetResponse{
		Organization:        org,
		OrganizationCreated: orgCreated,
		Project:             project,
		ProjectCreated:      !projectCreated,
		Environment:         environment,
		EnvironmentCreated:  !environmentCreated,
	}, nil
}

func (c *Client) resolveLegacyOrganization(ctx context.Context, token string, orgs []Organization, input string, preferredOrganizationID int) (Organization, bool, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed != "" {
		if org, ok := findOrganizationByInput(orgs, trimmed); ok {
			return org, false, nil
		}
		org, err := c.CreateOrganization(ctx, token, trimmed)
		return org, true, err
	}

	switch len(orgs) {
	case 0:
		org, err := c.CreateOrganization(ctx, token, "default")
		return org, true, err
	case 1:
		return orgs[0], false, nil
	}

	if preferredOrganizationID > 0 {
		for _, org := range orgs {
			if org.ID == preferredOrganizationID {
				return org, false, nil
			}
		}
	}

	return Organization{}, false, &StatusError{StatusCode: http.StatusUnprocessableEntity, Message: "multiple organizations available; pass organization or preferred_organization_id"}
}

func findOrganizationByInput(orgs []Organization, input string) (Organization, bool) {
	if id, err := strconv.Atoi(input); err == nil {
		for _, org := range orgs {
			if org.ID == id {
				return org, true
			}
		}
	}
	for _, org := range orgs {
		if org.Name == input {
			return org, true
		}
	}
	return Organization{}, false
}

func findProjectByName(projects []Project, name string) (Project, bool) {
	for _, project := range projects {
		if project.Name == name {
			return project, true
		}
	}
	return Project{}, false
}

func findEnvironmentByName(environments []Environment, name string) (Environment, bool) {
	for _, environment := range environments {
		if environment.Name == name {
			return environment, true
		}
	}
	return Environment{}, false
}

func (c *Client) DeleteEnvironment(ctx context.Context, token string, environmentID int) (map[string]any, error) {
	var body map[string]any
	err := c.request(ctx, http.MethodDelete, token, fmt.Sprintf("/api/v1/cli/environments/%d", environmentID), nil, nil, &body)
	return body, err
}

func (c *Client) EnvironmentStatus(ctx context.Context, token string, environmentID int) (map[string]any, error) {
	var body map[string]any
	err := c.get(ctx, token, fmt.Sprintf("/api/v1/cli/environments/%d/status", environmentID), nil, &body)
	return body, err
}

func (c *Client) RequestGARPushAuth(ctx context.Context, token string, projectID int, repository string) (GARPushAuth, error) {
	var body GARPushAuth
	err := c.post(ctx, token, fmt.Sprintf("/api/v1/cli/projects/%d/gar/push_auth", projectID), map[string]any{"image_repository": repository}, &body)
	return body, err
}

func (c *Client) RequestRegistryPushAuth(ctx context.Context, token string, projectID int, repository string) (GARPushAuth, error) {
	var body GARPushAuth
	err := c.post(ctx, token, fmt.Sprintf("/api/v1/cli/projects/%d/registry/push_auth", projectID), map[string]any{"image_repository": repository}, &body)
	if statusErr, ok := err.(*StatusError); ok && statusErr.StatusCode == http.StatusNotFound {
		return c.RequestGARPushAuth(ctx, token, projectID, repository)
	}
	return body, err
}

func (c *Client) GetOrganizationRegistry(ctx context.Context, token string, organizationID int) (OrganizationRegistryConfig, error) {
	var body OrganizationRegistryConfig
	err := c.get(ctx, token, fmt.Sprintf("/api/v1/cli/organizations/%d/registry", organizationID), nil, &body)
	return body, err
}

func (c *Client) UpsertOrganizationRegistry(ctx context.Context, token string, organizationID int, request map[string]any) (OrganizationRegistryConfig, error) {
	var body OrganizationRegistryConfig
	err := c.post(ctx, token, fmt.Sprintf("/api/v1/cli/organizations/%d/registry", organizationID), request, &body)
	return body, err
}

func (c *Client) CreateRelease(ctx context.Context, token string, projectID int, request ReleaseCreateRequest) (map[string]any, error) {
	var body map[string]any
	err := c.post(ctx, token, fmt.Sprintf("/api/v1/cli/projects/%d/releases", projectID), request, &body)
	return body, err
}

func (c *Client) PublishRelease(ctx context.Context, token string, releaseID, environmentID int, requestToken string) (map[string]any, error) {
	var body map[string]any
	err := c.post(ctx, token, fmt.Sprintf("/api/v1/cli/releases/%d/publish", releaseID), map[string]any{
		"environment_id": environmentID,
		"request_token":  requestToken,
	}, &body)
	return body, err
}

func (c *Client) CreateToken(ctx context.Context, token, refreshToken, name string) (ApiToken, error) {
	var body ApiToken
	err := c.post(ctx, token, "/api/v1/cli/tokens", map[string]any{"name": name, "refresh_token": refreshToken}, &body)
	return body, err
}

func (c *Client) ListTokens(ctx context.Context, token string) ([]ApiToken, error) {
	var body struct {
		Tokens []ApiToken `json:"tokens"`
	}
	err := c.get(ctx, token, "/api/v1/cli/tokens", nil, &body)
	return body.Tokens, err
}

func (c *Client) RevokeToken(ctx context.Context, token string, tokenID int) (ApiToken, error) {
	var body ApiToken
	err := c.request(ctx, http.MethodDelete, token, fmt.Sprintf("/api/v1/cli/tokens/%d", tokenID), nil, nil, &body)
	return body, err
}

func (c *Client) StartAccountClaim(ctx context.Context, token, email string) (map[string]any, error) {
	var body map[string]any
	err := c.post(ctx, token, "/api/v1/cli/account/claim/start", map[string]any{"email": email}, &body)
	return body, err
}

func (c *Client) DeploymentProgress(ctx context.Context, token string, deploymentID int) (DeploymentProgress, error) {
	var body DeploymentProgress
	err := c.get(ctx, token, fmt.Sprintf("/api/v1/cli/deployments/%d", deploymentID), nil, &body)
	return body, err
}

func (c *Client) ListNodes(ctx context.Context, token string, organizationID int) ([]Node, error) {
	var body struct {
		Nodes []Node `json:"nodes"`
	}
	err := c.get(ctx, token, fmt.Sprintf("/api/v1/cli/organizations/%d/nodes", organizationID), nil, &body)
	return body.Nodes, err
}

func (c *Client) CreateNodeBootstrapToken(ctx context.Context, token string, organizationID int, environmentID int) (map[string]any, error) {
	payload := map[string]any{}
	if environmentID > 0 {
		payload["environment_id"] = environmentID
	}
	var body map[string]any
	err := c.post(ctx, token, fmt.Sprintf("/api/v1/cli/organizations/%d/node_bootstrap_tokens", organizationID), payload, &body)
	return body, err
}

func (c *Client) UpdateNodeLabels(ctx context.Context, token string, nodeID int, labels string) (map[string]any, error) {
	var body map[string]any
	err := c.post(ctx, token, fmt.Sprintf("/api/v1/cli/nodes/%d/labels", nodeID), map[string]any{"labels": labels}, &body)
	return body, err
}

func (c *Client) DeleteNodeAssignment(ctx context.Context, token string, nodeID int) (map[string]any, error) {
	var body map[string]any
	err := c.request(ctx, http.MethodDelete, token, fmt.Sprintf("/api/v1/cli/nodes/%d/assignment", nodeID), nil, nil, &body)
	return body, err
}

func (c *Client) DeleteNode(ctx context.Context, token string, nodeID int) (map[string]any, error) {
	var body map[string]any
	err := c.request(ctx, http.MethodDelete, token, fmt.Sprintf("/api/v1/cli/nodes/%d", nodeID), nil, nil, &body)
	return body, err
}

func (c *Client) CreateNodeDiagnoseRequest(ctx context.Context, token string, nodeID int) (NodeDiagnoseRequest, error) {
	var body NodeDiagnoseRequest
	err := c.post(ctx, token, fmt.Sprintf("/api/v1/cli/nodes/%d/diagnose_requests", nodeID), map[string]any{}, &body)
	return body, err
}

func (c *Client) GetNodeDiagnoseRequest(ctx context.Context, token string, requestID int) (NodeDiagnoseRequest, error) {
	var body NodeDiagnoseRequest
	err := c.get(ctx, token, fmt.Sprintf("/api/v1/cli/node_diagnose_requests/%d", requestID), nil, &body)
	return body, err
}

func (c *Client) CreateEnvironmentAssignment(ctx context.Context, token string, environmentID int, nodeID int, onProgress func(string)) (map[string]any, error) {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(fmt.Sprintf("/api/v1/cli/environments/%d/assignments", environmentID))
	if err != nil {
		return nil, err
	}
	uri := base.ResolveReference(endpoint)

	data, err := json.Marshal(map[string]any{"node_id": nodeID})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uri.String(), bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the devopsellence API at %s: %w", uri.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var errBody map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		if message, _ := errBody["error_description"].(string); strings.TrimSpace(message) != "" {
			return nil, &StatusError{StatusCode: resp.StatusCode, Message: message}
		}
		if message, _ := errBody["error"].(string); strings.TrimSpace(message) != "" {
			return nil, &StatusError{StatusCode: resp.StatusCode, Message: message}
		}
		return nil, &StatusError{StatusCode: resp.StatusCode}
	}

	return parseAssignmentSSE(resp.Body, onProgress)
}

func parseAssignmentSSE(body io.Reader, onProgress func(string)) (map[string]any, error) {
	scanner := bufio.NewScanner(body)
	var eventType, dataLine string

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case line == "":
			if eventType != "" && dataLine != "" {
				switch eventType {
				case "progress":
					if onProgress != nil {
						var event struct {
							Message string `json:"message"`
						}
						if err := json.Unmarshal([]byte(dataLine), &event); err == nil {
							onProgress(event.Message)
						}
					}
				case "complete":
					var result map[string]any
					if err := json.Unmarshal([]byte(dataLine), &result); err != nil {
						return nil, fmt.Errorf("API returned invalid JSON: %w", err)
					}
					return result, nil
				case "error":
					var event struct {
						Message string `json:"message"`
					}
					if err := json.Unmarshal([]byte(dataLine), &event); err == nil && event.Message != "" {
						return nil, &StatusError{StatusCode: http.StatusServiceUnavailable, Message: event.Message}
					}
					return nil, &StatusError{StatusCode: http.StatusServiceUnavailable}
				}
			}
			eventType = ""
			dataLine = ""
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("unexpected end of stream from server")
}

func (c *Client) UpsertEnvironmentSecret(ctx context.Context, token string, environmentID int, serviceName, name, value string) (map[string]any, error) {
	var body map[string]any
	err := c.post(ctx, token, fmt.Sprintf("/api/v1/cli/environments/%d/secrets", environmentID), map[string]any{
		"service_name": serviceName,
		"name":         name,
		"value":        value,
	}, &body)
	return body, err
}

func (c *Client) ListEnvironmentSecrets(ctx context.Context, token string, environmentID int) ([]EnvironmentSecret, error) {
	var body struct {
		Secrets []EnvironmentSecret `json:"secrets"`
	}
	err := c.get(ctx, token, fmt.Sprintf("/api/v1/cli/environments/%d/secrets", environmentID), nil, &body)
	return body.Secrets, err
}

func (c *Client) DeleteEnvironmentSecret(ctx context.Context, token string, environmentID int, serviceName, name string) (map[string]any, error) {
	var body map[string]any
	err := c.request(ctx, http.MethodDelete, token, fmt.Sprintf(
		"/api/v1/cli/environments/%d/secrets/%s/%s",
		environmentID,
		url.PathEscape(serviceName),
		url.PathEscape(name),
	), nil, nil, &body)
	return body, err
}

func (c *Client) get(ctx context.Context, token, path string, params map[string]string, out any) error {
	return c.request(ctx, http.MethodGet, token, path, params, nil, out)
}

func (c *Client) post(ctx context.Context, token, path string, payload any, out any) error {
	return c.request(ctx, http.MethodPost, token, path, nil, payload, out)
}

func (c *Client) request(ctx context.Context, method, token, path string, params map[string]string, payload any, out any) error {
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return err
	}
	endpoint, err := url.Parse(path)
	if err != nil {
		return err
	}
	uri := base.ResolveReference(endpoint)
	query := uri.Query()
	for key, value := range params {
		if strings.TrimSpace(value) != "" {
			query.Set(key, value)
		}
	}
	uri.RawQuery = query.Encode()

	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, uri.String(), body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the devopsellence API at %s: %w", uri.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var errBody map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		if message, _ := errBody["error_description"].(string); strings.TrimSpace(message) != "" {
			return &StatusError{StatusCode: resp.StatusCode, Message: message}
		}
		if message, _ := errBody["error"].(string); strings.TrimSpace(message) != "" {
			return &StatusError{StatusCode: resp.StatusCode, Message: message}
		}
		return &StatusError{StatusCode: resp.StatusCode}
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("API returned invalid JSON: %w", err)
	}
	return nil
}
