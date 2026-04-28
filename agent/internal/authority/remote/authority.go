package remote

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/auth"
	"github.com/devopsellence/devopsellence/agent/internal/authority"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatecache"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/version"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	desiredStateEnvelopeFmt = "signed_desired_state.v1"
	desiredStatePointerFmt  = "desired_state_pointer.v1"
	desiredStateJWKSPath    = "/.well-known/devopsellence-desired-state-jwks.json"
)

type Config struct {
	GCSAPIEndpoint           string
	SecretManagerEndpoint    string
	DesiredStateCachePath    string
	DesiredStateOverridePath string
	HTTPClient               *http.Client
}

type Authority struct {
	cfg                Config
	auth               *auth.Manager
	logger             *slog.Logger
	httpClient         *http.Client
	now                func() time.Time
	beforeFetchForTest func()

	cachedURI         string
	cachedResolvedURI string
	cachedGeneration  string
	cachedETag        string
	cachedSequence    int64
	cachedDesired     *desiredstatepb.DesiredState
	publicKeys        map[string]*rsa.PublicKey
	cacheStore        *desiredstatecache.Store
	overrideDigest    string
	overridePresent   bool
	overrideModTime   time.Time
	overrideSize      int64
	overrideActive    bool
	overrideDesired   *desiredstatepb.DesiredState
	overrideErr       error
	fallbackMode      string
	lastFallbackLog   time.Time
}

type gcsObjectMetadata struct {
	Generation string `json:"generation"`
	ETag       string `json:"etag"`
}

type secretManagerAccessResponse struct {
	Payload struct {
		Data string `json:"data"`
	} `json:"payload"`
}

var errDesiredStateSourceUnavailable = errors.New("desired state source unavailable")

type desiredStateEnvelope struct {
	Format                  string `json:"format"`
	SchemaVersion           int64  `json:"schema_version"`
	Algorithm               string `json:"algorithm"`
	KeyID                   string `json:"key_id"`
	OrganizationBundleToken string `json:"organization_bundle_token"`
	EnvironmentBundleToken  string `json:"environment_bundle_token"`
	NodeBundleToken         string `json:"node_bundle_token"`
	NodeID                  int64  `json:"node_id"`
	EnvironmentID           int64  `json:"environment_id"`
	Sequence                int64  `json:"sequence"`
	IssuedAt                string `json:"issued_at"`
	ExpiresAt               string `json:"expires_at"`
	PayloadSHA256           string `json:"payload_sha256"`
	PayloadJSON             string `json:"payload_json"`
	Signature               string `json:"signature"`
}

type desiredStatePointer struct {
	Format        string `json:"format"`
	SchemaVersion int64  `json:"schema_version"`
	Sequence      int64  `json:"sequence"`
	ObjectPath    string `json:"object_path"`
	ObjectURI     string `json:"object_uri"`
	PublishedAt   string `json:"published_at"`
}

type jwksResponse struct {
	Keys []jwkKey `json:"keys"`
}

type jwkKey struct {
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func New(cfg Config, authManager *auth.Manager, logger *slog.Logger) *Authority {
	if logger == nil {
		logger = slog.Default()
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	cfg.GCSAPIEndpoint = strings.TrimRight(strings.TrimSpace(cfg.GCSAPIEndpoint), "/")
	if cfg.GCSAPIEndpoint == "" {
		cfg.GCSAPIEndpoint = "https://storage.googleapis.com"
	}
	cfg.SecretManagerEndpoint = strings.TrimRight(strings.TrimSpace(cfg.SecretManagerEndpoint), "/")
	if cfg.SecretManagerEndpoint == "" {
		cfg.SecretManagerEndpoint = "https://secretmanager.googleapis.com/v1"
	}

	return &Authority{
		cfg:        cfg,
		auth:       authManager,
		logger:     logger,
		httpClient: httpClient,
		now:        time.Now,
		publicKeys: map[string]*rsa.PublicKey{},
		cacheStore: desiredstatecache.New(cfg.DesiredStateCachePath),
	}
}

func (a *Authority) Fetch(ctx context.Context) (*authority.FetchResult, error) {
	if desired, err, ok := a.loadDesiredStateOverride(); ok {
		a.resetFallbackMode(false)
		if err != nil {
			return nil, err
		}
		return &authority.FetchResult{Desired: desired}, nil
	}
	if a.auth == nil {
		a.resetFallbackMode(false)
		return nil, authority.ErrNoDesiredState
	}
	snapshot := a.auth.DesiredStateSnapshot()
	if a.beforeFetchForTest != nil {
		a.beforeFetchForTest()
	}
	target := snapshot.Target
	if len(target.Inline) > 0 {
		a.resetCache("")
		a.resetFallbackMode(true)
		desired, sequence, err := a.parseDesiredStateEnvelope(ctx, snapshot, target.Inline, "", "")
		if err != nil {
			return nil, err
		}
		return &authority.FetchResult{Desired: desired, Sequence: sequence}, nil
	}

	targetURI := strings.TrimSpace(target.URI)
	if targetURI == "" {
		a.resetCache("")
		a.resetFallbackMode(false)
		return nil, authority.ErrNoDesiredState
	}
	if targetURI != a.cachedURI {
		a.resetCache(targetURI)
	}
	desired, generation, etag, sequence, resolvedURI, err := a.fetchDesiredState(ctx, snapshot, targetURI)
	if err != nil {
		if fallback, ok := a.loadFallbackDesiredState(snapshot, err); ok {
			return &authority.FetchResult{
				Desired:  fallback,
				Sequence: a.cachedSequence,
			}, nil
		}
		return nil, err
	}
	a.resetFallbackMode(true)
	if desired != nil {
		a.cachedDesired = desired
		a.cachedResolvedURI = resolvedURI
		a.cachedGeneration = generation
		a.cachedETag = etag
		a.cachedSequence = sequence
		if err := a.cacheDesiredState(snapshot, sequence, desired); err != nil {
			a.logger.Warn("persist desired state cache failed", "error", err, "uri", targetURI)
		}
	}

	if a.cachedDesired == nil {
		return nil, authority.ErrNoDesiredState
	}
	if a.cachedSequence < snapshot.SequenceFloor {
		return nil, fmt.Errorf("desired state envelope sequence rollback: got %d want >= %d", a.cachedSequence, snapshot.SequenceFloor)
	}
	return &authority.FetchResult{
		Desired:  a.cachedDesired,
		Sequence: a.cachedSequence,
	}, nil
}

func (a *Authority) fetchDesiredState(ctx context.Context, snapshot auth.DesiredStateSnapshot, targetURI string) (*desiredstatepb.DesiredState, string, string, int64, string, error) {
	parsed, err := url.Parse(targetURI)
	if err != nil {
		return nil, "", "", 0, "", fmt.Errorf("invalid desired state uri %q: %w", targetURI, err)
	}

	switch parsed.Scheme {
	case "gs":
		token, _, err := a.auth.GoogleAccess(ctx)
		if err != nil {
			return nil, "", "", 0, "", fmt.Errorf("%w: google access: %v", errDesiredStateSourceUnavailable, err)
		}
		metadata, err := a.readGCSMetadata(ctx, parsed, token)
		if err != nil {
			return nil, "", "", 0, "", fmt.Errorf("%w: read gcs metadata: %v", errDesiredStateSourceUnavailable, err)
		}
		if a.cachedDesired != nil && metadata.Generation == a.cachedGeneration && metadata.ETag == a.cachedETag {
			if a.cachedSequence < snapshot.SequenceFloor {
				return nil, metadata.Generation, metadata.ETag, a.cachedSequence, a.cachedResolvedURI, fmt.Errorf("desired state envelope sequence rollback: got %d want >= %d", a.cachedSequence, snapshot.SequenceFloor)
			}
			return nil, metadata.Generation, metadata.ETag, a.cachedSequence, a.cachedResolvedURI, nil
		}
		data, err := a.readGCSMedia(ctx, parsed, token)
		if err != nil {
			return nil, "", "", 0, "", fmt.Errorf("%w: read gcs media: %v", errDesiredStateSourceUnavailable, err)
		}
		desired, sequence, resolvedURI, err := a.parseDesiredStateDocument(ctx, snapshot, parsed, targetURI, data, token, "")
		if err != nil {
			return nil, "", "", 0, "", err
		}
		return desired, metadata.Generation, metadata.ETag, sequence, resolvedURI, nil
	case "http", "https":
		etag, statusCode, data, err := a.readHTTPDesiredState(ctx, targetURI)
		if err != nil {
			return nil, "", "", 0, "", fmt.Errorf("%w: read control-plane desired state: %v", errDesiredStateSourceUnavailable, err)
		}
		if statusCode == http.StatusNotModified {
			if a.cachedDesired == nil {
				return nil, "", etag, 0, "", errors.New("control-plane desired state returned not modified without cached desired state")
			}
			if a.cachedSequence < snapshot.SequenceFloor {
				return nil, "", etag, a.cachedSequence, a.cachedResolvedURI, fmt.Errorf("desired state envelope sequence rollback: got %d want >= %d", a.cachedSequence, snapshot.SequenceFloor)
			}
			return nil, "", etag, a.cachedSequence, a.cachedResolvedURI, nil
		}
		desired, sequence, resolvedURI, err := a.parseDesiredStateDocument(ctx, snapshot, parsed, targetURI, data, "", "")
		if err != nil {
			return nil, "", "", 0, "", err
		}
		return desired, "", etag, sequence, resolvedURI, nil
	default:
		return nil, "", "", 0, "", fmt.Errorf("unsupported desired state uri scheme: %s", parsed.Scheme)
	}
}

func (a *Authority) parseDesiredStateDocument(ctx context.Context, snapshot auth.DesiredStateSnapshot, sourceURI *url.URL, sourceURIString string, data []byte, googleToken string, controlPlaneToken string) (*desiredstatepb.DesiredState, int64, string, error) {
	var header struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return nil, 0, "", fmt.Errorf("parse desired state document: %w", err)
	}

	switch strings.TrimSpace(header.Format) {
	case desiredStatePointerFmt:
		return a.parseDesiredStatePointer(ctx, snapshot, sourceURI, sourceURIString, data, googleToken, controlPlaneToken)
	case desiredStateEnvelopeFmt:
		desired, sequence, err := a.parseDesiredStateEnvelope(ctx, snapshot, data, googleToken, controlPlaneToken)
		return desired, sequence, sourceURIString, err
	default:
		return nil, 0, "", fmt.Errorf("unexpected desired state document format %q", header.Format)
	}
}

func (a *Authority) parseDesiredStatePointer(ctx context.Context, snapshot auth.DesiredStateSnapshot, sourceURI *url.URL, sourceURIString string, data []byte, googleToken string, controlPlaneToken string) (*desiredstatepb.DesiredState, int64, string, error) {
	var pointer desiredStatePointer
	if err := json.Unmarshal(data, &pointer); err != nil {
		return nil, 0, "", fmt.Errorf("parse desired state pointer: %w", err)
	}
	if strings.TrimSpace(pointer.Format) != desiredStatePointerFmt {
		return nil, 0, "", fmt.Errorf("unexpected desired state pointer format %q", pointer.Format)
	}
	if pointer.Sequence < snapshot.SequenceFloor {
		return nil, 0, "", fmt.Errorf("desired state envelope sequence rollback: got %d want >= %d", pointer.Sequence, snapshot.SequenceFloor)
	}

	resolvedURIString, resolvedURI, err := resolveDesiredStatePointer(sourceURI, sourceURIString, pointer)
	if err != nil {
		return nil, 0, "", err
	}
	if a.cachedDesired != nil && a.cachedResolvedURI == resolvedURIString && a.cachedSequence == pointer.Sequence {
		return a.cachedDesired, a.cachedSequence, resolvedURIString, nil
	}

	envelopeData, err := a.readGCSMedia(ctx, resolvedURI, googleToken)
	if err != nil {
		return nil, 0, "", fmt.Errorf("%w: read pointed gcs media: %v", errDesiredStateSourceUnavailable, err)
	}
	desired, sequence, err := a.parseDesiredStateEnvelope(ctx, snapshot, envelopeData, googleToken, controlPlaneToken)
	if err != nil {
		return nil, 0, "", err
	}
	if sequence != pointer.Sequence {
		return nil, 0, "", fmt.Errorf("desired state pointer sequence mismatch: got %d want %d", sequence, pointer.Sequence)
	}
	return desired, sequence, resolvedURIString, nil
}

func resolveDesiredStatePointer(sourceURI *url.URL, sourceURIString string, pointer desiredStatePointer) (string, *url.URL, error) {
	objectURI := strings.TrimSpace(pointer.ObjectURI)
	if objectURI != "" {
		parsed, err := url.Parse(objectURI)
		if err != nil {
			return "", nil, fmt.Errorf("invalid desired state pointer object_uri %q: %w", objectURI, err)
		}
		if parsed.Scheme != "gs" {
			return "", nil, fmt.Errorf("unsupported desired state pointer object_uri scheme %q", parsed.Scheme)
		}
		if strings.TrimSpace(parsed.Host) != strings.TrimSpace(sourceURI.Host) {
			return "", nil, fmt.Errorf("desired state pointer object_uri bucket mismatch: got %q want %q", parsed.Host, sourceURI.Host)
		}
		if objectURI == sourceURIString {
			return "", nil, errors.New("desired state pointer object_uri must differ from source uri")
		}
		return objectURI, parsed, nil
	}

	objectPath := strings.TrimSpace(pointer.ObjectPath)
	if objectPath == "" {
		return "", nil, errors.New("desired state pointer missing object_path")
	}

	resolved := &url.URL{
		Scheme: "gs",
		Host:   sourceURI.Host,
		Path:   "/" + strings.TrimPrefix(objectPath, "/"),
	}
	resolvedURIString := resolved.String()
	if resolvedURIString == sourceURIString {
		return "", nil, errors.New("desired state pointer object_path must differ from source uri")
	}
	return resolvedURIString, resolved, nil
}

func (a *Authority) parseDesiredStateEnvelope(ctx context.Context, snapshot auth.DesiredStateSnapshot, data []byte, googleToken string, controlPlaneToken string) (*desiredstatepb.DesiredState, int64, error) {
	var envelope desiredStateEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, 0, fmt.Errorf("parse desired state envelope: %w", err)
	}
	if strings.TrimSpace(envelope.Format) != desiredStateEnvelopeFmt {
		return nil, 0, fmt.Errorf("unexpected desired state envelope format %q", envelope.Format)
	}
	if strings.TrimSpace(envelope.Algorithm) != "RS256" {
		return nil, 0, fmt.Errorf("unsupported desired state envelope algorithm %q", envelope.Algorithm)
	}
	if strings.TrimSpace(envelope.KeyID) == "" {
		return nil, 0, errors.New("desired state envelope missing key_id")
	}
	if strings.TrimSpace(envelope.PayloadJSON) == "" {
		return nil, 0, errors.New("desired state envelope missing payload_json")
	}
	if strings.TrimSpace(envelope.PayloadSHA256) == "" {
		return nil, 0, errors.New("desired state envelope missing payload_sha256")
	}
	if strings.TrimSpace(envelope.Signature) == "" {
		return nil, 0, errors.New("desired state envelope missing signature")
	}
	payloadDigest := sha256.Sum256([]byte(envelope.PayloadJSON))
	if fmt.Sprintf("%x", payloadDigest[:]) != strings.ToLower(strings.TrimSpace(envelope.PayloadSHA256)) {
		return nil, 0, errors.New("desired state envelope payload hash mismatch")
	}
	issuedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(envelope.IssuedAt))
	if err != nil {
		return nil, 0, fmt.Errorf("parse desired state envelope issued_at: %w", err)
	}
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(envelope.ExpiresAt))
	if err != nil {
		return nil, 0, fmt.Errorf("parse desired state envelope expires_at: %w", err)
	}
	if !a.now().Before(expiresAt) {
		return nil, 0, errors.New("desired state envelope expired")
	}
	if envelope.Sequence < snapshot.SequenceFloor {
		return nil, 0, fmt.Errorf("desired state envelope sequence rollback: got %d want >= %d", envelope.Sequence, snapshot.SequenceFloor)
	}
	target := snapshot.Target
	if target.OrganizationBundleToken != "" && envelope.OrganizationBundleToken != target.OrganizationBundleToken {
		return nil, 0, fmt.Errorf("desired state envelope organization_bundle_token mismatch: got %q want %q", envelope.OrganizationBundleToken, target.OrganizationBundleToken)
	}
	if target.EnvironmentBundleToken != "" && envelope.EnvironmentBundleToken != target.EnvironmentBundleToken {
		return nil, 0, fmt.Errorf("desired state envelope environment_bundle_token mismatch: got %q want %q", envelope.EnvironmentBundleToken, target.EnvironmentBundleToken)
	}
	if target.NodeBundleToken != "" && envelope.NodeBundleToken != target.NodeBundleToken {
		return nil, 0, fmt.Errorf("desired state envelope node_bundle_token mismatch: got %q want %q", envelope.NodeBundleToken, target.NodeBundleToken)
	}
	if target.NodeBundleToken == "" {
		if snapshot.NodeID > 0 && envelope.NodeID != snapshot.NodeID {
			return nil, 0, fmt.Errorf("desired state envelope node_id mismatch: got %d want %d", envelope.NodeID, snapshot.NodeID)
		}
		if snapshot.EnvironmentID > 0 && envelope.EnvironmentID != snapshot.EnvironmentID {
			return nil, 0, fmt.Errorf("desired state envelope environment_id mismatch: got %d want %d", envelope.EnvironmentID, snapshot.EnvironmentID)
		}
	}
	publicKey, err := a.publicKeyFor(ctx, envelope.KeyID)
	if err != nil {
		return nil, 0, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(envelope.Signature))
	if err != nil {
		return nil, 0, fmt.Errorf("decode desired state envelope signature: %w", err)
	}
	signingInput := buildDesiredStateSigningInput(
		envelope.OrganizationBundleToken,
		envelope.EnvironmentBundleToken,
		envelope.NodeBundleToken,
		envelope.NodeID,
		envelope.EnvironmentID,
		envelope.Sequence,
		issuedAt,
		expiresAt,
		envelope.PayloadSHA256,
	)
	signatureDigest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, signatureDigest[:], signature); err != nil {
		return nil, 0, fmt.Errorf("verify desired state envelope signature: %w", err)
	}
	var desired desiredstatepb.DesiredState
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(envelope.PayloadJSON), &desired); err != nil {
		return nil, 0, fmt.Errorf("parse desired state payload: %w", err)
	}
	if err := a.resolveSecretRefs(ctx, &desired, googleToken, controlPlaneToken); err != nil {
		return nil, 0, fmt.Errorf("resolve secret refs: %w", err)
	}
	if err := a.auth.RecordDesiredStateSequenceFloorForSnapshot(snapshot, envelope.Sequence); err != nil {
		return nil, 0, fmt.Errorf("persist desired state sequence floor: %w", err)
	}
	return &desired, envelope.Sequence, nil
}

func (a *Authority) resolveSecretRefs(ctx context.Context, desired *desiredstatepb.DesiredState, googleToken string, controlPlaneToken string) error {
	for _, env := range desired.Environments {
		if env == nil {
			continue
		}
		for _, service := range env.Services {
			if service == nil {
				continue
			}
			if len(service.SecretRefs) == 0 {
				continue
			}
			if service.Env == nil {
				service.Env = map[string]string{}
			}
			for key, ref := range service.SecretRefs {
				if _, exists := service.Env[key]; exists {
					return fmt.Errorf("service[%s/%s]: env key %q conflicts with secret_ref", env.Name, service.Name, key)
				}
				value, resolvedGoogleToken, resolvedControlPlaneToken, err := a.resolveSecretRef(ctx, ref, googleToken, controlPlaneToken)
				if err != nil {
					return fmt.Errorf("service[%s/%s] secret_ref[%s]: %w", env.Name, service.Name, key, err)
				}
				googleToken = resolvedGoogleToken
				controlPlaneToken = resolvedControlPlaneToken
				service.Env[key] = value
			}
			service.SecretRefs = nil
		}
		for _, task := range env.Tasks {
			if task == nil {
				continue
			}
			if len(task.SecretRefs) == 0 {
				continue
			}
			if task.Env == nil {
				task.Env = map[string]string{}
			}
			for key, ref := range task.SecretRefs {
				if _, exists := task.Env[key]; exists {
					return fmt.Errorf("task[%s/%s]: env key %q conflicts with secret_ref", env.Name, task.Name, key)
				}
				value, resolvedGoogleToken, resolvedControlPlaneToken, err := a.resolveSecretRef(ctx, ref, googleToken, controlPlaneToken)
				if err != nil {
					return fmt.Errorf("task[%s/%s] secret_ref[%s]: %w", env.Name, task.Name, key, err)
				}
				googleToken = resolvedGoogleToken
				controlPlaneToken = resolvedControlPlaneToken
				task.Env[key] = value
			}
			task.SecretRefs = nil
		}
	}
	return nil
}

func (a *Authority) resolveSecretRef(ctx context.Context, ref string, googleToken string, controlPlaneToken string) (string, string, string, error) {
	parsed, err := url.Parse(ref)
	if err != nil {
		return "", googleToken, controlPlaneToken, fmt.Errorf("invalid secret ref %q: %w", ref, err)
	}

	switch parsed.Scheme {
	case "gsm":
		if strings.TrimSpace(googleToken) == "" {
			token, _, err := a.auth.GoogleAccess(ctx)
			if err != nil {
				return "", googleToken, controlPlaneToken, err
			}
			googleToken = token
		}
		resource, err := parseSecretManagerResource(parsed)
		if err != nil {
			return "", googleToken, controlPlaneToken, err
		}

		encodedPath := encodePath(resource)
		target := fmt.Sprintf("%s/%s:access", a.cfg.SecretManagerEndpoint, encodedPath)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return "", googleToken, controlPlaneToken, fmt.Errorf("build secretmanager request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+googleToken)
		req.Header.Set("Accept", "application/json")

		var response secretManagerAccessResponse
		if err := a.doJSON(req, &response); err != nil {
			return "", googleToken, controlPlaneToken, fmt.Errorf("secretmanager access failed: %w", err)
		}

		if strings.TrimSpace(response.Payload.Data) == "" {
			return "", googleToken, controlPlaneToken, errors.New("secretmanager response missing payload.data")
		}
		decoded, err := base64.StdEncoding.DecodeString(response.Payload.Data)
		if err != nil {
			return "", googleToken, controlPlaneToken, fmt.Errorf("decode secret payload: %w", err)
		}
		value := normalizeSecretValue(decoded)
		if value == "" {
			return "", googleToken, controlPlaneToken, errors.New("secret payload is empty")
		}
		return value, googleToken, controlPlaneToken, nil
	case "http", "https":
		if strings.TrimSpace(controlPlaneToken) == "" {
			token, _, err := a.auth.ControlPlaneAccess(ctx)
			if err != nil {
				return "", googleToken, controlPlaneToken, err
			}
			controlPlaneToken = token
		}
		value, err := a.resolveHTTPSecretRef(ctx, ref, controlPlaneToken)
		if err != nil {
			return "", googleToken, controlPlaneToken, err
		}
		return value, googleToken, controlPlaneToken, nil
	default:
		return "", googleToken, controlPlaneToken, fmt.Errorf("unsupported secret ref scheme: %q", ref)
	}
}

func parseSecretManagerResource(uri *url.URL) (string, error) {
	resource := strings.TrimPrefix(strings.TrimSpace(uri.Host+uri.Path), "/")
	parts := strings.Split(resource, "/")
	if len(parts) != 6 || parts[0] != "projects" || parts[2] != "secrets" || parts[4] != "versions" {
		return "", fmt.Errorf("invalid gsm ref resource: %s", uri.String())
	}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return "", fmt.Errorf("invalid gsm ref resource: %s", uri.String())
		}
	}
	return resource, nil
}

func (a *Authority) readGCSMetadata(ctx context.Context, uri *url.URL, bearerToken string) (gcsObjectMetadata, error) {
	bucket, object, err := parseGCSObject(uri)
	if err != nil {
		return gcsObjectMetadata{}, err
	}

	target := fmt.Sprintf("%s/storage/v1/b/%s/o/%s?fields=generation%%2Cetag", a.cfg.GCSAPIEndpoint, url.PathEscape(bucket), url.PathEscape(object))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return gcsObjectMetadata{}, fmt.Errorf("build gcs metadata request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)
	req.Header.Set("Accept", "application/json")

	var metadata gcsObjectMetadata
	if err := a.doJSON(req, &metadata); err != nil {
		return gcsObjectMetadata{}, fmt.Errorf("gcs metadata request failed: %w", err)
	}
	return metadata, nil
}

func (a *Authority) readGCSMedia(ctx context.Context, uri *url.URL, bearerToken string) ([]byte, error) {
	bucket, object, err := parseGCSObject(uri)
	if err != nil {
		return nil, err
	}

	target := fmt.Sprintf("%s/storage/v1/b/%s/o/%s?alt=media", a.cfg.GCSAPIEndpoint, url.PathEscape(bucket), url.PathEscape(object))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("build gcs media request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearerToken)

	data, err := a.doBytes(req)
	if err != nil {
		return nil, fmt.Errorf("gcs media request failed: %w", err)
	}
	return data, nil
}

func (a *Authority) loadDesiredStateOverride() (*desiredstatepb.DesiredState, error, bool) {
	path := strings.TrimSpace(a.cfg.DesiredStateOverridePath)
	if path == "" {
		a.clearOverrideState()
		return nil, nil, false
	}

	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if a.overridePresent {
				a.logger.Info("local desired state override cleared", "path", path)
			}
			a.clearOverrideState()
			return nil, nil, false
		}
		return nil, fmt.Errorf("read local desired state override: %w", err), true
	}
	if a.overridePresent && info.Size() == a.overrideSize && info.ModTime().Equal(a.overrideModTime) {
		switch {
		case a.overrideErr != nil:
			return nil, a.overrideErr, true
		case !a.overrideActive:
			return nil, nil, false
		default:
			return a.overrideDesired, nil, true
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if a.overridePresent {
				a.logger.Info("local desired state override cleared", "path", path)
			}
			a.clearOverrideState()
			return nil, nil, false
		}
		return nil, fmt.Errorf("read local desired state override: %w", err), true
	}

	prevDigest := a.overrideDigest
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	desired, active, err := desiredstatecache.ParseOverride(data)
	a.overridePresent = true
	a.overrideModTime = info.ModTime()
	a.overrideSize = info.Size()
	if err != nil {
		a.overrideDesired = nil
		a.overrideActive = false
		a.overrideDigest = ""
		a.overrideErr = err
		return nil, err, true
	}
	a.overrideErr = nil
	if !active {
		if prevDigest != "" || a.overrideActive {
			a.logger.Info("local desired state override disabled", "path", path)
		}
		a.overrideDesired = nil
		a.overrideActive = false
		a.overrideDigest = ""
		return nil, nil, false
	}

	a.overrideDesired = desired
	a.overrideActive = true
	a.overrideDigest = digest
	if digest != prevDigest {
		a.logger.Warn("using local desired state override", "path", path)
	}
	return desired, nil, true
}

func (a *Authority) cacheDesiredState(snapshot auth.DesiredStateSnapshot, sequence int64, desired *desiredstatepb.DesiredState) error {
	if strings.TrimSpace(snapshot.Target.URI) == "" {
		return nil
	}
	if a.cacheStore == nil {
		return nil
	}
	return a.cacheStore.Save(snapshot, sequence, desired)
}

func (a *Authority) loadFallbackDesiredState(snapshot auth.DesiredStateSnapshot, cause error) (*desiredstatepb.DesiredState, bool) {
	if !errors.Is(cause, errDesiredStateSourceUnavailable) {
		return nil, false
	}

	if a.cachedDesired != nil && cachedDesiredMatchesSnapshot(a.cachedURI, snapshot) {
		if a.cachedSequence < snapshot.SequenceFloor {
			a.logFallback("stale_in_memory", "using stale in-memory desired state cache", a.cachedURI, a.cachedSequence, snapshot.SequenceFloor, cause)
		} else {
			a.logFallback("in_memory", "using in-memory desired state cache", a.cachedURI, a.cachedSequence, snapshot.SequenceFloor, cause)
		}
		return a.cachedDesired, true
	}

	if a.cacheStore == nil {
		return nil, false
	}
	entry, desired, err := a.cacheStore.Load()
	if err != nil {
		a.logger.Warn("load desired state cache failed", "error", err)
		return nil, false
	}
	if entry == nil || desired == nil {
		return nil, false
	}
	if !persistedCacheMatchesSnapshot(entry, snapshot) {
		return nil, false
	}

	a.cachedURI = entry.URI
	a.cachedGeneration = ""
	a.cachedETag = ""
	a.cachedSequence = entry.Sequence
	a.cachedDesired = desired

	if entry.Sequence < snapshot.SequenceFloor {
		a.logFallback("stale_persisted", "using stale persisted desired state cache", entry.URI, entry.Sequence, snapshot.SequenceFloor, cause)
	} else {
		a.logFallback("persisted", "using persisted desired state cache", entry.URI, entry.Sequence, snapshot.SequenceFloor, cause)
	}
	return desired, true
}

func (a *Authority) clearOverrideState() {
	a.overrideDigest = ""
	a.overridePresent = false
	a.overrideModTime = time.Time{}
	a.overrideSize = 0
	a.overrideActive = false
	a.overrideDesired = nil
	a.overrideErr = nil
}

func (a *Authority) logFallback(mode, message, uri string, sequence, floor int64, cause error) {
	now := a.now()
	if a.fallbackMode == mode && !a.lastFallbackLog.IsZero() && now.Sub(a.lastFallbackLog) < time.Minute {
		return
	}
	attrs := []any{"uri", uri, "sequence", sequence, "error", cause}
	if floor > 0 {
		attrs = append(attrs, "sequence_floor", floor)
	}
	a.logger.Warn(message, attrs...)
	a.fallbackMode = mode
	a.lastFallbackLog = now
}

func (a *Authority) resetFallbackMode(logRecovery bool) {
	if a.fallbackMode == "" {
		return
	}
	if logRecovery {
		a.logger.Info("authoritative desired state source restored", "previous_mode", a.fallbackMode)
	}
	a.fallbackMode = ""
	a.lastFallbackLog = time.Time{}
}

func cachedDesiredMatchesSnapshot(uri string, snapshot auth.DesiredStateSnapshot) bool {
	targetURI := strings.TrimSpace(snapshot.Target.URI)
	if targetURI == "" {
		return false
	}
	return strings.TrimSpace(uri) == targetURI
}

func persistedCacheMatchesSnapshot(entry *desiredstatecache.Entry, snapshot auth.DesiredStateSnapshot) bool {
	targetURI := strings.TrimSpace(snapshot.Target.URI)
	if targetURI == "" || entry == nil || strings.TrimSpace(entry.URI) != targetURI {
		return false
	}

	if snapshot.Target.OrganizationBundleToken != "" || snapshot.Target.EnvironmentBundleToken != "" || snapshot.Target.NodeBundleToken != "" {
		return strings.TrimSpace(entry.OrganizationBundleToken) == snapshot.Target.OrganizationBundleToken &&
			strings.TrimSpace(entry.EnvironmentBundleToken) == snapshot.Target.EnvironmentBundleToken &&
			strings.TrimSpace(entry.NodeBundleToken) == snapshot.Target.NodeBundleToken
	}

	if snapshot.NodeID > 0 && entry.NodeID != snapshot.NodeID {
		return false
	}
	if snapshot.EnvironmentID > 0 && entry.EnvironmentID != snapshot.EnvironmentID {
		return false
	}
	return true
}

func parseGCSObject(uri *url.URL) (string, string, error) {
	bucket := strings.TrimSpace(uri.Host)
	object := strings.TrimPrefix(strings.TrimSpace(uri.Path), "/")
	if bucket == "" || object == "" {
		return "", "", fmt.Errorf("invalid gcs uri: %s", uri.String())
	}
	return bucket, object, nil
}

func (a *Authority) readHTTPDesiredState(ctx context.Context, targetURI string) (string, int, []byte, error) {
	token, _, err := a.auth.ControlPlaneAccess(ctx)
	if err != nil {
		return "", 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURI, nil)
	if err != nil {
		return "", 0, nil, fmt.Errorf("build desired state request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	if a.cachedETag != "" {
		req.Header.Set("If-None-Match", a.cachedETag)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", 0, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	etag := strings.TrimSpace(resp.Header.Get("ETag"))
	if resp.StatusCode == http.StatusNotModified {
		return etag, resp.StatusCode, nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return "", resp.StatusCode, nil, &httpStatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, nil, fmt.Errorf("read desired state response: %w", err)
	}
	return etag, resp.StatusCode, body, nil
}

func (a *Authority) resolveHTTPSecretRef(ctx context.Context, ref string, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ref, nil)
	if err != nil {
		return "", fmt.Errorf("build secret request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	var response struct {
		Value string `json:"value"`
	}
	if err := a.doJSON(req, &response); err != nil {
		return "", fmt.Errorf("secret request failed: %w", err)
	}
	value := normalizeSecretValue([]byte(response.Value))
	if value == "" {
		return "", errors.New("secret response missing value")
	}
	return value, nil
}

func (a *Authority) doJSON(req *http.Request, out any) error {
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return &httpStatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}

func (a *Authority) doBytes(req *http.Request) ([]byte, error) {
	req.Header.Set("User-Agent", version.UserAgent())
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, &httpStatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	return io.ReadAll(resp.Body)
}

func (a *Authority) resetCache(targetURI string) {
	a.cachedURI = targetURI
	a.cachedResolvedURI = ""
	a.cachedGeneration = ""
	a.cachedETag = ""
	a.cachedSequence = 0
	a.cachedDesired = nil
}

func encodePath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func normalizeSecretValue(data []byte) string {
	value := string(data)
	value = strings.TrimSuffix(value, "\n")
	value = strings.TrimSuffix(value, "\r")
	return value
}

func buildDesiredStateSigningInput(organizationBundleToken, environmentBundleToken, nodeBundleToken string, nodeID, environmentID, sequence int64, issuedAt, expiresAt time.Time, payloadSHA256 string) string {
	return strings.Join([]string{
		desiredStateEnvelopeFmt,
		fmt.Sprintf("organization_bundle_token=%s", organizationBundleToken),
		fmt.Sprintf("environment_bundle_token=%s", environmentBundleToken),
		fmt.Sprintf("node_bundle_token=%s", nodeBundleToken),
		fmt.Sprintf("node_id=%d", nodeID),
		fmt.Sprintf("environment_id=%d", environmentID),
		fmt.Sprintf("sequence=%d", sequence),
		fmt.Sprintf("issued_at=%s", issuedAt.UTC().Format(time.RFC3339)),
		fmt.Sprintf("expires_at=%s", expiresAt.UTC().Format(time.RFC3339)),
		fmt.Sprintf("payload_sha256=%s", payloadSHA256),
	}, "\n")
}

func (a *Authority) publicKeyFor(ctx context.Context, keyID string) (*rsa.PublicKey, error) {
	if key, ok := a.publicKeys[keyID]; ok {
		return key, nil
	}
	target := strings.TrimRight(a.auth.BaseURL(), "/") + desiredStateJWKSPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("build jwks request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	var response jwksResponse
	if err := a.doJSON(req, &response); err != nil {
		return nil, fmt.Errorf("jwks request failed: %w", err)
	}
	for _, key := range response.Keys {
		publicKey, err := parseJWK(key)
		if err != nil {
			continue
		}
		a.publicKeys[key.Kid] = publicKey
	}
	key, ok := a.publicKeys[keyID]
	if !ok {
		return nil, fmt.Errorf("desired state signing key %q not found in jwks", keyID)
	}
	return key, nil
}

func parseJWK(key jwkKey) (*rsa.PublicKey, error) {
	if strings.TrimSpace(key.Kty) != "RSA" || strings.TrimSpace(key.N) == "" || strings.TrimSpace(key.E) == "" {
		return nil, errors.New("invalid jwk")
	}
	modulusBytes, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, err
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, err
	}
	exponent := 0
	for _, b := range exponentBytes {
		exponent = exponent<<8 + int(b)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(modulusBytes),
		E: exponent,
	}, nil
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
