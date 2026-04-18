package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	FilePath               = "devopsellence.yml"
	GenericFilePath        = FilePath
	SchemaVersion          = 4
	DefaultEnvironment     = "production"
	DefaultBuildContext    = "."
	DefaultDockerfile      = "Dockerfile"
	DefaultHealthcheckPath = "/up"
	DefaultWebPort         = 3000
	AppTypeRails           = "rails"
	AppTypeGeneric         = "generic"
	NodeLabelWeb           = "web"
	NodeLabelWorker        = "worker"
)

var DefaultBuildPlatforms = []string{"linux/amd64"}
var SoloDefaultLabels = []string{NodeLabelWeb}

type Volume struct {
	Source string `yaml:"source" json:"source"`
	Target string `yaml:"target" json:"target"`
}

type SecretRef struct {
	Name   string `yaml:"name" json:"name"`
	Secret string `yaml:"secret" json:"secret"`
}

type HTTPHealthcheck struct {
	Path string `yaml:"path,omitempty" json:"path,omitempty"`
	Port int    `yaml:"port,omitempty" json:"port,omitempty"`
}

type ServiceConfig struct {
	Entrypoint  string            `yaml:"entrypoint,omitempty" json:"entrypoint,omitempty"`
	Command     string            `yaml:"command,omitempty" json:"command,omitempty"`
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	SecretRefs  []SecretRef       `yaml:"secret_refs,omitempty" json:"secret_refs,omitempty"`
	Port        int               `yaml:"port,omitempty" json:"port,omitempty"`
	Healthcheck *HTTPHealthcheck  `yaml:"healthcheck,omitempty" json:"healthcheck,omitempty"`
	Volumes     []Volume          `yaml:"volumes,omitempty" json:"volumes,omitempty"`
}

type Service = ServiceConfig

type BuildConfig struct {
	Context    string   `yaml:"context" json:"context"`
	Dockerfile string   `yaml:"dockerfile" json:"dockerfile"`
	Platforms  []string `yaml:"platforms" json:"platforms"`
}

type AppConfig struct {
	Type string `yaml:"type,omitempty" json:"type,omitempty"`
}

type IngressTLSConfig struct {
	Mode           string `yaml:"mode,omitempty" json:"mode,omitempty"`
	Email          string `yaml:"email,omitempty" json:"email,omitempty"`
	CADirectoryURL string `yaml:"ca_directory_url,omitempty" json:"ca_directory_url,omitempty"`
}

type IngressConfig struct {
	Hosts        []string         `yaml:"hosts,omitempty" json:"hosts,omitempty"`
	TLS          IngressTLSConfig `yaml:"tls,omitempty" json:"tls,omitempty"`
	RedirectHTTP bool             `yaml:"redirect_http,omitempty" json:"redirect_http,omitempty"`
}

type SoloNode struct {
	Host             string   `yaml:"host" json:"host"`
	User             string   `yaml:"user" json:"user"`
	Port             int      `yaml:"port,omitempty" json:"port,omitempty"`
	SSHKey           string   `yaml:"ssh_key,omitempty" json:"ssh_key,omitempty"`
	AgentStateDir    string   `yaml:"agent_state_dir,omitempty" json:"agent_state_dir,omitempty"`
	Labels           []string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Provider         string   `yaml:"provider,omitempty" json:"provider,omitempty"`
	ProviderServerID string   `yaml:"provider_server_id,omitempty" json:"provider_server_id,omitempty"`
	ProviderRegion   string   `yaml:"provider_region,omitempty" json:"provider_region,omitempty"`
	ProviderSize     string   `yaml:"provider_size,omitempty" json:"provider_size,omitempty"`
	ProviderImage    string   `yaml:"provider_image,omitempty" json:"provider_image,omitempty"`
}

type SoloConfig struct {
	Nodes map[string]SoloNode `yaml:"nodes" json:"nodes"`
}

type ProjectConfig struct {
	SchemaVersion      int            `yaml:"schema_version" json:"schema_version"`
	App                AppConfig      `yaml:"app,omitempty" json:"app,omitempty"`
	Organization       string         `yaml:"organization" json:"organization"`
	Project            string         `yaml:"project" json:"project"`
	DefaultEnvironment string         `yaml:"default_environment" json:"default_environment"`
	Build              BuildConfig    `yaml:"build" json:"build"`
	Web                ServiceConfig  `yaml:"web" json:"web"`
	Worker             *ServiceConfig `yaml:"worker,omitempty" json:"worker,omitempty"`
	ReleaseCommand     string         `yaml:"release_command,omitempty" json:"release_command,omitempty"`
	Ingress            *IngressConfig `yaml:"ingress,omitempty" json:"ingress,omitempty"`
	Solo               *SoloConfig    `yaml:"solo,omitempty" json:"solo,omitempty"`
}

type Project = ProjectConfig

type Store struct{}

func NewStore() Store {
	return Store{}
}

func (Store) PathFor(workspaceRoot string) string {
	if path, ok := ExistingPath(workspaceRoot); ok {
		return path
	}
	return filepath.Join(workspaceRoot, FilePath)
}

func (Store) PathForType(workspaceRoot, appType string) string {
	return PathForType(workspaceRoot, appType)
}

func (s Store) Read(workspaceRoot string) (*ProjectConfig, error) {
	return LoadFromRoot(workspaceRoot)
}

func Load(path string) (*ProjectConfig, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cfg ProjectConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", filepath.Base(path), err)
	}
	if strings.TrimSpace(cfg.App.Type) == "" && filepath.Base(path) == GenericFilePath {
		cfg.App.Type = AppTypeGeneric
	}
	if cfg.SchemaVersion == 0 {
		return nil, fmt.Errorf("invalid %s in %s: schema_version must be %d; re-run `devopsellence setup`", filepath.Base(path), path, SchemaVersion)
	}
	applyDefaults(&cfg)
	if err := Validate(&cfg); err != nil {
		return nil, fmt.Errorf("invalid %s in %s: %w", filepath.Base(path), path, err)
	}
	return &cfg, nil
}

func ExistingPath(workspaceRoot string) (string, bool) {
	candidates := []string{
		filepath.Join(workspaceRoot, FilePath),
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

func PathForType(workspaceRoot, _ string) string {
	return filepath.Join(workspaceRoot, FilePath)
}

func LoadFromRoot(workspaceRoot string) (*ProjectConfig, error) {
	path, ok := ExistingPath(workspaceRoot)
	if !ok {
		return nil, nil
	}
	return Load(path)
}

func (s Store) Fetch(workspaceRoot string) (ProjectConfig, error) {
	cfg, err := s.Read(workspaceRoot)
	if err != nil {
		return ProjectConfig{}, err
	}
	if cfg == nil {
		return ProjectConfig{}, fmt.Errorf("project not initialized. run `devopsellence setup` from %s", workspaceRoot)
	}
	return *cfg, nil
}

func (s Store) Write(workspaceRoot string, cfg ProjectConfig) (ProjectConfig, error) {
	return Write(workspaceRoot, cfg)
}

func Write(workspaceRoot string, cfg ProjectConfig) (ProjectConfig, error) {
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = SchemaVersion
	}
	applyDefaults(&cfg)
	if err := Validate(&cfg); err != nil {
		return ProjectConfig{}, err
	}

	path := PathForType(workspaceRoot, cfg.App.Type)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ProjectConfig{}, err
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return ProjectConfig{}, err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return ProjectConfig{}, err
	}
	return cfg, nil
}

func DefaultProjectConfig(organization, project, environment string) ProjectConfig {
	return DefaultProjectConfigForType(organization, project, environment, AppTypeRails)
}

func DefaultProjectConfigForType(organization, project, environment, appType string) ProjectConfig {
	healthcheckPath := DefaultHealthcheckPath
	if appType == AppTypeGeneric {
		healthcheckPath = "/"
	}
	cfg := ProjectConfig{
		SchemaVersion:      SchemaVersion,
		App:                AppConfig{Type: appType},
		Organization:       organization,
		Project:            project,
		DefaultEnvironment: environment,
		Build: BuildConfig{
			Context:    DefaultBuildContext,
			Dockerfile: DefaultDockerfile,
			Platforms:  append([]string(nil), DefaultBuildPlatforms...),
		},
		Web: ServiceConfig{
			Env:        map[string]string{},
			SecretRefs: []SecretRef{},
			Volumes:    []Volume{},
			Port:       DefaultWebPort,
			Healthcheck: &HTTPHealthcheck{
				Path: healthcheckPath,
				Port: DefaultWebPort,
			},
		},
	}
	applyDefaults(&cfg)
	return cfg
}

func Validate(cfg *ProjectConfig) error {
	if cfg == nil {
		return errors.New("config is required")
	}
	if cfg.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version must be %d; re-run `devopsellence setup`", SchemaVersion)
	}
	if cfg.App.Type != AppTypeRails && cfg.App.Type != AppTypeGeneric {
		return fmt.Errorf("app.type must be %q or %q", AppTypeRails, AppTypeGeneric)
	}
	if strings.TrimSpace(cfg.Organization) == "" {
		return errors.New("organization is required")
	}
	if strings.TrimSpace(cfg.Project) == "" {
		return errors.New("project is required")
	}
	if strings.TrimSpace(cfg.DefaultEnvironment) == "" {
		return errors.New("default_environment is required")
	}
	if strings.TrimSpace(cfg.Build.Context) == "" {
		return errors.New("build.context is required")
	}
	if strings.TrimSpace(cfg.Build.Dockerfile) == "" {
		return errors.New("build.dockerfile is required")
	}
	if len(cfg.Build.Platforms) == 0 {
		return errors.New("build.platforms must include at least one platform")
	}
	for _, platform := range cfg.Build.Platforms {
		if strings.TrimSpace(platform) == "" {
			return errors.New("build.platforms entries must be present")
		}
	}
	if err := validateService("web", cfg.Web, true); err != nil {
		return err
	}
	if cfg.Worker != nil {
		if err := validateService("worker", *cfg.Worker, false); err != nil {
			return err
		}
	}
	if cfg.Ingress != nil {
		if len(cfg.Ingress.Hosts) == 0 {
			return errors.New("ingress.hosts must include at least one host")
		}
		seenHosts := map[string]bool{}
		for _, host := range cfg.Ingress.Hosts {
			host = strings.TrimSpace(host)
			if host == "" {
				return errors.New("ingress.hosts entries must be present")
			}
			if seenHosts[host] {
				return fmt.Errorf("ingress.hosts contains duplicate host %q", host)
			}
			seenHosts[host] = true
		}
		switch strings.TrimSpace(cfg.Ingress.TLS.Mode) {
		case "", "auto", "off", "manual":
		default:
			return fmt.Errorf("ingress.tls.mode must be auto, off, or manual")
		}
	}
	if cfg.Solo != nil {
		for name, node := range cfg.Solo.Nodes {
			if strings.TrimSpace(name) == "" {
				return errors.New("solo.nodes keys must be present")
			}
			if strings.TrimSpace(node.Host) == "" {
				return fmt.Errorf("solo.nodes.%s.host is required", name)
			}
			if strings.TrimSpace(node.User) == "" {
				return fmt.Errorf("solo.nodes.%s.user is required", name)
			}
			if len(node.Labels) == 0 {
				return fmt.Errorf("solo.nodes.%s.labels must include at least one label", name)
			}
			for _, label := range node.Labels {
				switch strings.TrimSpace(label) {
				case NodeLabelWeb, NodeLabelWorker:
				case "":
					return fmt.Errorf("solo.nodes.%s.labels entries must be present", name)
				default:
					return fmt.Errorf("solo.nodes.%s.labels contains unsupported label %q", name, label)
				}
			}
		}
	}
	return nil
}

func applyDefaults(cfg *ProjectConfig) {
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = SchemaVersion
	}
	if strings.TrimSpace(cfg.App.Type) == "" {
		cfg.App.Type = AppTypeRails
	}
	if strings.TrimSpace(cfg.DefaultEnvironment) == "" {
		cfg.DefaultEnvironment = DefaultEnvironment
	}
	if strings.TrimSpace(cfg.Build.Context) == "" {
		cfg.Build.Context = DefaultBuildContext
	}
	if strings.TrimSpace(cfg.Build.Dockerfile) == "" {
		cfg.Build.Dockerfile = DefaultDockerfile
	}
	if len(cfg.Build.Platforms) == 0 {
		cfg.Build.Platforms = append([]string(nil), DefaultBuildPlatforms...)
	}
	if cfg.Web.Env == nil {
		cfg.Web.Env = map[string]string{}
	}
	if cfg.Web.SecretRefs == nil {
		cfg.Web.SecretRefs = []SecretRef{}
	}
	if cfg.Web.Volumes == nil {
		cfg.Web.Volumes = []Volume{}
	}
	if cfg.Web.Port == 0 {
		cfg.Web.Port = DefaultWebPort
	}
	if cfg.Web.Healthcheck == nil {
		cfg.Web.Healthcheck = &HTTPHealthcheck{}
	}
	if strings.TrimSpace(cfg.Web.Healthcheck.Path) == "" {
		if cfg.App.Type == AppTypeGeneric {
			cfg.Web.Healthcheck.Path = "/"
		} else {
			cfg.Web.Healthcheck.Path = DefaultHealthcheckPath
		}
	}
	if cfg.Web.Healthcheck.Port == 0 {
		cfg.Web.Healthcheck.Port = cfg.Web.Port
	}
	if cfg.Worker != nil {
		if cfg.Worker.Env == nil {
			cfg.Worker.Env = map[string]string{}
		}
		if cfg.Worker.SecretRefs == nil {
			cfg.Worker.SecretRefs = []SecretRef{}
		}
		if cfg.Worker.Volumes == nil {
			cfg.Worker.Volumes = []Volume{}
		}
	}
	if cfg.Ingress != nil {
		cfg.Ingress.Hosts = normalizeStringList(cfg.Ingress.Hosts)
		cfg.Ingress.TLS.Mode = strings.TrimSpace(cfg.Ingress.TLS.Mode)
		if cfg.Ingress.TLS.Mode == "" {
			cfg.Ingress.TLS.Mode = "auto"
		}
		cfg.Ingress.TLS.Email = strings.TrimSpace(cfg.Ingress.TLS.Email)
		cfg.Ingress.TLS.CADirectoryURL = strings.TrimSpace(cfg.Ingress.TLS.CADirectoryURL)
		if cfg.Ingress.TLS.Mode == "auto" {
			cfg.Ingress.RedirectHTTP = true
		}
	}
	if cfg.Solo != nil {
		if cfg.Solo.Nodes == nil {
			cfg.Solo.Nodes = map[string]SoloNode{}
		}
		for name, node := range cfg.Solo.Nodes {
			if node.Port == 0 {
				node.Port = 22
			}
			if node.AgentStateDir == "" {
				node.AgentStateDir = "/var/lib/devopsellence"
			}
			labels := normalizeNodeLabels(node.Labels)
			if len(labels) == 0 {
				labels = append([]string(nil), SoloDefaultLabels...)
			}
			node.Labels = append([]string(nil), labels...)
			cfg.Solo.Nodes[name] = node
		}
	}
}

func normalizeNodeLabels(labels []string) []string {
	if labels == nil {
		return nil
	}
	seen := make(map[string]bool, len(labels))
	normalized := make([]string, 0, len(labels))
	for _, label := range labels {
		label = strings.TrimSpace(label)
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		normalized = append(normalized, label)
	}
	return normalized
}

func normalizeStringList(values []string) []string {
	seen := make(map[string]bool, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	return normalized
}

func validateService(name string, service ServiceConfig, allowHealthcheck bool) error {
	for key := range service.Env {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s.env keys must be present", name)
		}
	}
	for _, secret := range service.SecretRefs {
		if strings.TrimSpace(secret.Name) == "" {
			return fmt.Errorf("%s.secret_refs[].name is required", name)
		}
		if strings.TrimSpace(secret.Secret) == "" {
			return fmt.Errorf("%s.secret_refs[].secret is required", name)
		}
	}
	for _, volume := range service.Volumes {
		if strings.TrimSpace(volume.Source) == "" {
			return fmt.Errorf("%s.volumes[].source is required", name)
		}
		if strings.TrimSpace(volume.Target) == "" {
			return fmt.Errorf("%s.volumes[].target is required", name)
		}
		if !filepath.IsAbs(volume.Target) {
			return fmt.Errorf("%s.volumes[].target must be absolute", name)
		}
	}
	if allowHealthcheck {
		if service.Port <= 0 {
			return fmt.Errorf("%s.port must be a positive integer", name)
		}
		if service.Healthcheck == nil {
			return fmt.Errorf("%s.healthcheck is required", name)
		}
		if strings.TrimSpace(service.Healthcheck.Path) == "" {
			return fmt.Errorf("%s.healthcheck.path is required", name)
		}
		if service.Healthcheck.Port <= 0 {
			return fmt.Errorf("%s.healthcheck.port must be a positive integer", name)
		}
	} else if service.Port != 0 || service.Healthcheck != nil {
		return fmt.Errorf("%s cannot define port or healthcheck settings", name)
	}
	return nil
}
