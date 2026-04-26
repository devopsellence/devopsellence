package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/devopsellence/cli/internal/api"
	"github.com/devopsellence/cli/internal/state"
	"github.com/devopsellence/cli/internal/version"
)

const (
	callbackPath    = "/callback"
	defaultTimeout  = 5 * time.Minute
	accessTokenSkew = 30 * time.Second
)

type Tokens struct {
	AccessToken     string `json:"access_token"`
	RefreshToken    string `json:"refresh_token"`
	TokenType       string `json:"token_type"`
	APIBase         string `json:"api_base"`
	ExpiresAt       string `json:"expires_at"`
	AccountKind     string `json:"account_kind"`
	AnonymousID     string `json:"anonymous_id"`
	AnonymousSecret string `json:"anonymous_secret"`
}

type Manager struct {
	APIBase   string
	LoginBase string
	Store     *state.Store
	Client    *http.Client
	OpenURL   func(string) error
}

func New(store *state.Store, apiBase, loginBase string) *Manager {
	if strings.TrimSpace(apiBase) == "" {
		apiBase = api.DefaultBaseURL
	}
	if strings.TrimSpace(loginBase) == "" {
		loginBase = apiBase
	}
	return &Manager{
		APIBase:   strings.TrimRight(apiBase, "/"),
		LoginBase: strings.TrimRight(loginBase, "/"),
		Store:     store,
		Client:    http.DefaultClient,
		OpenURL:   openBrowser,
	}
}

func (m *Manager) ReadState() (Tokens, error) {
	if m.Store == nil {
		return Tokens{}, nil
	}
	value, err := m.Store.Read()
	if err != nil {
		return Tokens{}, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return Tokens{}, err
	}
	var tokens Tokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return Tokens{}, err
	}
	return tokens, nil
}

func (m *Manager) AccessTokenValid(tokens Tokens) bool {
	if strings.TrimSpace(tokens.ExpiresAt) == "" {
		return false
	}
	expiresAt, err := time.Parse(time.RFC3339, tokens.ExpiresAt)
	if err != nil {
		return false
	}
	return expiresAt.After(time.Now().Add(accessTokenSkew))
}

func (m *Manager) EnsureAuthenticated(ctx context.Context, interactive bool, allowAnonymousCreate bool, notify func(string)) (Tokens, error) {
	if token := strings.TrimSpace(os.Getenv("DEVOPSELLENCE_TOKEN")); token != "" {
		return Tokens{AccessToken: token, APIBase: m.APIBase}, nil
	}

	tokens, err := m.ReadState()
	if err != nil {
		return Tokens{}, err
	}
	if m.AccessTokenValid(tokens) {
		return tokens, nil
	}
	if strings.TrimSpace(tokens.RefreshToken) != "" {
		refreshed, err := m.Refresh(ctx, tokens)
		if err == nil {
			return refreshed, nil
		}
	}
	if m.hasAnonymousCredentials(tokens) {
		return m.BootstrapAnonymous(ctx, tokens, notify)
	}
	if allowAnonymousCreate {
		anonymous := Tokens{
			AnonymousID:     randomString(16),
			AnonymousSecret: randomString(32),
			APIBase:         firstNonEmpty(tokens.APIBase, m.APIBase),
		}
		if notify != nil {
			notify("Creating anonymous trial account…")
		}
		return m.BootstrapAnonymous(ctx, anonymous, notify)
	}
	if !interactive {
		return Tokens{}, errors.New("authentication required; provide DEVOPSELLENCE_TOKEN or run `devopsellence auth login` before invoking agent workflows")
	}
	return m.Login(ctx, notify)
}

func (m *Manager) Refresh(ctx context.Context, tokens Tokens) (Tokens, error) {
	payload := map[string]any{"refresh_token": tokens.RefreshToken, "client_id": "cli"}
	endpoint := m.APIBase + "/api/v1/cli/auth/refresh"
	result, err := m.post(ctx, endpoint, payload)
	if err != nil {
		return Tokens{}, err
	}
	next, err := m.tokensFromResponse(result)
	if err != nil {
		return Tokens{}, err
	}
	return next, m.persist(next)
}

func (m *Manager) Login(ctx context.Context, notify func(string)) (Tokens, error) {
	if notify == nil {
		notify = func(string) {}
	}
	notify("Starting loopback callback server…")
	server, redirectURI, waitForCode, err := startLoopbackServer()
	if err != nil {
		return Tokens{}, err
	}
	defer server.Shutdown(context.Background())

	stateValue := randomString(16)
	codeVerifier := randomString(32)
	loginURL, err := m.loginURL(redirectURI, stateValue, codeVerifier)
	if err != nil {
		return Tokens{}, err
	}

	notify("Opening browser for sign-in…")
	if err := m.OpenURL(loginURL); err != nil {
		notify("Open this URL manually: " + loginURL)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	notify("Waiting for browser callback…")
	code, err := waitForCode(timeoutCtx, stateValue)
	if err != nil {
		return Tokens{}, err
	}

	notify("Exchanging auth code for API tokens…")
	payload := map[string]any{
		"code":          code,
		"redirect_uri":  redirectURI,
		"code_verifier": codeVerifier,
		"client_id":     "cli",
	}
	result, err := m.post(ctx, m.APIBase+"/api/v1/cli/auth/token", payload)
	if err != nil {
		return Tokens{}, err
	}
	tokens, err := m.tokensFromResponse(result)
	if err != nil {
		return Tokens{}, err
	}
	tokens.AnonymousID = ""
	tokens.AnonymousSecret = ""
	return tokens, m.persist(tokens)
}

func (m *Manager) BootstrapAnonymous(ctx context.Context, tokens Tokens, notify func(string)) (Tokens, error) {
	if !m.hasAnonymousCredentials(tokens) {
		return Tokens{}, errors.New("anonymous credentials are missing")
	}
	if notify != nil {
		notify("Authenticating anonymous trial account…")
	}

	result, err := m.post(ctx, m.APIBase+"/api/v1/public/cli/bootstrap", map[string]any{
		"anonymous_id":     tokens.AnonymousID,
		"anonymous_secret": tokens.AnonymousSecret,
		"client_id":        "cli",
	})
	if err != nil {
		return Tokens{}, err
	}
	next, err := m.tokensFromResponse(result)
	if err != nil {
		return Tokens{}, err
	}
	next.AnonymousID = tokens.AnonymousID
	next.AnonymousSecret = tokens.AnonymousSecret
	return next, m.persist(next)
}

func (m *Manager) Logout() (bool, error) {
	if m.Store == nil {
		return false, nil
	}
	return m.Store.Delete()
}

func (m *Manager) persist(tokens Tokens) error {
	if m.Store == nil {
		return nil
	}
	return m.Store.Update(func(current map[string]any) (map[string]any, error) {
		current["access_token"] = tokens.AccessToken
		current["refresh_token"] = tokens.RefreshToken
		current["token_type"] = tokens.TokenType
		current["api_base"] = firstNonEmpty(tokens.APIBase, m.APIBase)
		current["expires_at"] = tokens.ExpiresAt
		current["account_kind"] = tokens.AccountKind

		if strings.TrimSpace(tokens.AccountKind) == "human" {
			delete(current, "anonymous_id")
			delete(current, "anonymous_secret")
		} else {
			current["anonymous_id"] = firstNonEmpty(tokens.AnonymousID, stringValue(current["anonymous_id"]))
			current["anonymous_secret"] = firstNonEmpty(tokens.AnonymousSecret, stringValue(current["anonymous_secret"]))
		}
		return current, nil
	})
}

func (m *Manager) tokensFromResponse(body map[string]any) (Tokens, error) {
	accessToken, _ := body["access_token"].(string)
	refreshToken, _ := body["refresh_token"].(string)
	tokenType, _ := body["token_type"].(string)
	expiresIn, ok := body["expires_in"].(float64)
	if !ok {
		return Tokens{}, errors.New("token response missing expires_in")
	}
	if strings.TrimSpace(accessToken) == "" || strings.TrimSpace(refreshToken) == "" || strings.TrimSpace(tokenType) == "" {
		return Tokens{}, errors.New("token response missing required fields")
	}
	return Tokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		TokenType:    tokenType,
		APIBase:      m.APIBase,
		ExpiresAt:    time.Now().UTC().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339),
		AccountKind:  stringValue(body["account_kind"]),
	}, nil
}

func (m *Manager) hasAnonymousCredentials(tokens Tokens) bool {
	return strings.TrimSpace(tokens.AnonymousID) != "" && strings.TrimSpace(tokens.AnonymousSecret) != ""
}

func (m *Manager) loginURL(redirectURI, stateValue, codeVerifier string) (string, error) {
	base, err := url.Parse(m.LoginBase + "/login")
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(codeVerifier))
	query := base.Query()
	query.Set("redirect_uri", redirectURI)
	query.Set("state", stateValue)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(sum[:]))
	query.Set("code_challenge_method", "S256")
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func (m *Manager) post(ctx context.Context, endpoint string, payload map[string]any) (map[string]any, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := m.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach the devopsellence API at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("API returned invalid JSON: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if message, _ := body["error_description"].(string); strings.TrimSpace(message) != "" {
			return nil, errors.New(message)
		}
		if message, _ := body["error"].(string); strings.TrimSpace(message) != "" {
			return nil, errors.New(message)
		}
		return nil, fmt.Errorf("API request failed with status %d", resp.StatusCode)
	}
	return body, nil
}

func startLoopbackServer() (*http.Server, string, func(context.Context, string) (string, error), error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", nil, err
	}

	type result struct {
		code  string
		state string
	}
	results := make(chan result, 1)
	server := &http.Server{
		Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != callbackPath {
				http.NotFound(writer, request)
				return
			}
			results <- result{code: request.URL.Query().Get("code"), state: request.URL.Query().Get("state")}
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(writer, "<html><body><p>Signed in. You can close this window.</p></body></html>")
		}),
	}
	go server.Serve(listener)

	redirectURI := "http://" + listener.Addr().String() + callbackPath
	wait := func(ctx context.Context, expectedState string) (string, error) {
		select {
		case <-ctx.Done():
			return "", errors.New("login timed out. please retry")
		case result := <-results:
			if result.state != expectedState {
				return "", errors.New("login callback state mismatch")
			}
			if strings.TrimSpace(result.code) == "" {
				return "", errors.New("login callback missing code")
			}
			return result.code, nil
		}
	}
	return server, redirectURI, wait, nil
}

func randomString(size int) string {
	buffer := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, buffer); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buffer)
}

func openBrowser(target string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", target).Start()
	case "linux":
		return exec.Command("xdg-open", target).Start()
	default:
		return errors.New("automatic browser open is unavailable on this platform")
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
