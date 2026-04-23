package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	FilePath               = "devopsellence.yml"
	GenericFilePath        = FilePath
	SchemaVersion          = 6
	DefaultEnvironment     = "production"
	DefaultBuildContext    = "."
	DefaultDockerfile      = "Dockerfile"
	DefaultHealthcheckPath = "/up"
	DefaultWebPort         = 3000
	AppTypeRails           = "rails"
	AppTypeGeneric         = "generic"
	DefaultWebRole         = "web"
	DefaultWorkerRole      = "worker"
	DefaultWebServiceName  = "web"
	ServiceKindWeb         = "web"
	ServiceKindWorker      = "worker"
	ServiceKindAccessory   = "accessory"
)

var DefaultBuildPlatforms = []string{"linux/amd64"}
var SoloDefaultLabels = []string{DefaultWebRole}

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

type ServicePort struct {
	Name string `yaml:"name" json:"name"`
	Port int    `yaml:"port" json:"port"`
}

type ServiceConfig struct {
	Kind        string            `yaml:"kind" json:"kind"`
	Image       string            `yaml:"image,omitempty" json:"image,omitempty"`
	Command     []string          `yaml:"command,omitempty" json:"command,omitempty"`
	Args        []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env         map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	SecretRefs  []SecretRef       `yaml:"secret_refs,omitempty" json:"secret_refs,omitempty"`
	Ports       []ServicePort     `yaml:"ports,omitempty" json:"ports,omitempty"`
	Healthcheck *HTTPHealthcheck  `yaml:"healthcheck,omitempty" json:"healthcheck,omitempty"`
	Volumes     []Volume          `yaml:"volumes,omitempty" json:"volumes,omitempty"`
}

type Service = ServiceConfig

type TaskConfig struct {
	Service string            `yaml:"service" json:"service"`
	Command []string          `yaml:"command,omitempty" json:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

type TasksConfig struct {
	Release *TaskConfig `yaml:"release,omitempty" json:"release,omitempty"`
}

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
	Service      string           `yaml:"service,omitempty" json:"service,omitempty"`
	TLS          IngressTLSConfig `yaml:"tls,omitempty" json:"tls,omitempty"`
	RedirectHTTP bool             `yaml:"redirect_http,omitempty" json:"redirect_http,omitempty"`
}

type HTTPHealthcheckOverlay struct {
	Path *string `yaml:"path,omitempty" json:"path,omitempty"`
	Port *int    `yaml:"port,omitempty" json:"port,omitempty"`
}

type ServiceConfigOverlay struct {
	Kind        *string                 `yaml:"kind,omitempty" json:"kind,omitempty"`
	Image       *string                 `yaml:"image,omitempty" json:"image,omitempty"`
	Command     []string                `yaml:"command,omitempty" json:"command,omitempty"`
	Args        []string                `yaml:"args,omitempty" json:"args,omitempty"`
	Env         map[string]string       `yaml:"env,omitempty" json:"env,omitempty"`
	SecretRefs  []SecretRef             `yaml:"secret_refs,omitempty" json:"secret_refs,omitempty"`
	Ports       []ServicePort           `yaml:"ports,omitempty" json:"ports,omitempty"`
	Healthcheck *HTTPHealthcheckOverlay `yaml:"healthcheck,omitempty" json:"healthcheck,omitempty"`
	Volumes     []Volume                `yaml:"volumes,omitempty" json:"volumes,omitempty"`
}

type TaskConfigOverlay struct {
	Service *string           `yaml:"service,omitempty" json:"service,omitempty"`
	Command []string          `yaml:"command,omitempty" json:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

type TasksConfigOverlay struct {
	Release *TaskConfigOverlay `yaml:"release,omitempty" json:"release,omitempty"`
}

type IngressTLSConfigOverlay struct {
	Mode           *string `yaml:"mode,omitempty" json:"mode,omitempty"`
	Email          *string `yaml:"email,omitempty" json:"email,omitempty"`
	CADirectoryURL *string `yaml:"ca_directory_url,omitempty" json:"ca_directory_url,omitempty"`
}

type IngressConfigOverlay struct {
	Hosts        []string                 `yaml:"hosts,omitempty" json:"hosts,omitempty"`
	Service      *string                  `yaml:"service,omitempty" json:"service,omitempty"`
	TLS          *IngressTLSConfigOverlay `yaml:"tls,omitempty" json:"tls,omitempty"`
	RedirectHTTP *bool                    `yaml:"redirect_http,omitempty" json:"redirect_http,omitempty"`
}

type EnvironmentOverlay struct {
	Ingress  *IngressConfigOverlay           `yaml:"ingress,omitempty" json:"ingress,omitempty"`
	Services map[string]ServiceConfigOverlay `yaml:"services,omitempty" json:"services,omitempty"`
	Tasks    *TasksConfigOverlay             `yaml:"tasks,omitempty" json:"tasks,omitempty"`
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

type ProjectConfig struct {
	SchemaVersion      int                           `yaml:"schema_version" json:"schema_version"`
	App                AppConfig                     `yaml:"app,omitempty" json:"app,omitempty"`
	Organization       string                        `yaml:"organization" json:"organization"`
	Project            string                        `yaml:"project" json:"project"`
	DefaultEnvironment string                        `yaml:"default_environment" json:"default_environment"`
	Build              BuildConfig                   `yaml:"build" json:"build"`
	Services           map[string]ServiceConfig      `yaml:"services" json:"services"`
	Tasks              TasksConfig                   `yaml:"tasks,omitempty" json:"tasks,omitempty"`
	Ingress            *IngressConfig                `yaml:"ingress,omitempty" json:"ingress,omitempty"`
	Environments       map[string]EnvironmentOverlay `yaml:"environments,omitempty" json:"environments,omitempty"`
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
		Services: map[string]ServiceConfig{
			DefaultWebServiceName: {
				Kind:       ServiceKindWeb,
				Env:        map[string]string{},
				SecretRefs: []SecretRef{},
				Volumes:    []Volume{},
				Ports: []ServicePort{{
					Name: "http",
					Port: DefaultWebPort,
				}},
				Healthcheck: &HTTPHealthcheck{
					Path: healthcheckPath,
					Port: DefaultWebPort,
				},
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
	if err := validateEnvironmentOverlays(cfg); err != nil {
		return err
	}
	resolved, err := ResolveEnvironmentConfig(*cfg, cfg.DefaultEnvironment)
	if err != nil {
		return err
	}
	return validateResolvedProjectConfig(&resolved)
}

func validateResolvedProjectConfig(cfg *ProjectConfig) error {
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
	if len(cfg.Services) == 0 {
		return errors.New("services must include at least one service")
	}
	webServices := 0
	for _, name := range cfg.ServiceNames() {
		service := cfg.Services[name]
		if err := validateService(name, service); err != nil {
			return err
		}
		if service.Kind == ServiceKindWeb {
			webServices++
		}
	}
	if webServices == 0 {
		return errors.New("services must include at least one web service")
	}
	if err := validateTasks(cfg); err != nil {
		return err
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
		if strings.TrimSpace(cfg.Ingress.Service) == "" {
			return errors.New("ingress.service is required")
		}
		service, ok := cfg.Services[cfg.Ingress.Service]
		if !ok {
			return fmt.Errorf("ingress.service %q not found in services", cfg.Ingress.Service)
		}
		if service.Kind != ServiceKindWeb {
			return fmt.Errorf("ingress.service %q must be kind %q", cfg.Ingress.Service, ServiceKindWeb)
		}
		switch strings.TrimSpace(cfg.Ingress.TLS.Mode) {
		case "", "auto", "off", "manual":
		default:
			return fmt.Errorf("ingress.tls.mode must be auto, off, or manual")
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
	if cfg.Services == nil {
		cfg.Services = map[string]ServiceConfig{}
	}
	if cfg.Environments == nil {
		cfg.Environments = map[string]EnvironmentOverlay{}
	}
	for name, service := range cfg.Services {
		if service.Env == nil {
			service.Env = map[string]string{}
		}
		if service.SecretRefs == nil {
			service.SecretRefs = []SecretRef{}
		}
		if service.Volumes == nil {
			service.Volumes = []Volume{}
		}
		service.Ports = normalizeServicePorts(service.Ports)
		if service.Kind == ServiceKindWeb {
			if len(service.Ports) == 0 {
				service.Ports = []ServicePort{{Name: "http", Port: DefaultWebPort}}
			}
			if service.Healthcheck == nil {
				service.Healthcheck = &HTTPHealthcheck{}
			}
			if strings.TrimSpace(service.Healthcheck.Path) == "" {
				if cfg.App.Type == AppTypeGeneric {
					service.Healthcheck.Path = "/"
				} else {
					service.Healthcheck.Path = DefaultHealthcheckPath
				}
			}
			if service.Healthcheck.Port == 0 {
				service.Healthcheck.Port = service.HTTPPort(DefaultWebPort)
			}
		}
		cfg.Services[name] = service
	}
	if cfg.Tasks.Release != nil {
		cfg.Tasks.Release.Service = strings.TrimSpace(cfg.Tasks.Release.Service)
		cfg.Tasks.Release.Command = normalizeStringListKeepOrder(cfg.Tasks.Release.Command)
		cfg.Tasks.Release.Args = normalizeStringListKeepOrder(cfg.Tasks.Release.Args)
		cfg.Tasks.Release.Env = mergeStringMaps(cfg.Tasks.Release.Env)
	}
	if cfg.Ingress != nil {
		cfg.Ingress.Hosts = normalizeStringList(cfg.Ingress.Hosts)
		cfg.Ingress.Service = strings.TrimSpace(cfg.Ingress.Service)
		cfg.Ingress.TLS.Mode = strings.TrimSpace(cfg.Ingress.TLS.Mode)
		if cfg.Ingress.TLS.Mode == "" {
			cfg.Ingress.TLS.Mode = "auto"
		}
		cfg.Ingress.TLS.Email = strings.TrimSpace(cfg.Ingress.TLS.Email)
		cfg.Ingress.TLS.CADirectoryURL = strings.TrimSpace(cfg.Ingress.TLS.CADirectoryURL)
	}
}

func ResolveEnvironmentConfig(base ProjectConfig, selectedEnv string) (ProjectConfig, error) {
	resolved := cloneProjectConfig(base)
	envName := strings.TrimSpace(selectedEnv)
	if envName == "" {
		envName = strings.TrimSpace(base.DefaultEnvironment)
	}
	if envName == "" {
		envName = DefaultEnvironment
	}
	resolved.DefaultEnvironment = envName

	overlay, ok := base.Environments[envName]
	if !ok {
		applyDefaults(&resolved)
		return resolved, nil
	}

	if overlay.Ingress != nil {
		resolved.Ingress = mergeIngressConfig(resolved.Ingress, overlay.Ingress)
	}
	if len(overlay.Services) > 0 {
		if resolved.Services == nil {
			resolved.Services = map[string]ServiceConfig{}
		}
		for name, entry := range overlay.Services {
			baseService := resolved.Services[name]
			resolved.Services[name] = mergeServiceConfig(baseService, entry)
		}
	}
	if overlay.Tasks != nil {
		resolved.Tasks = mergeTasksConfig(resolved.Tasks, overlay.Tasks)
	}

	applyDefaults(&resolved)
	return resolved, nil
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

func normalizeStringListKeepOrder(values []string) []string {
	if values == nil {
		return nil
	}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		normalized = append(normalized, strings.TrimSpace(value))
	}
	return normalized
}

func normalizeServicePorts(ports []ServicePort) []ServicePort {
	if ports == nil {
		return nil
	}
	seen := map[string]bool{}
	normalized := make([]ServicePort, 0, len(ports))
	for _, port := range ports {
		name := strings.TrimSpace(port.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		normalized = append(normalized, ServicePort{Name: name, Port: port.Port})
	}
	return normalized
}

func validateService(name string, service ServiceConfig) error {
	switch service.Kind {
	case ServiceKindWeb, ServiceKindWorker, ServiceKindAccessory:
	default:
		return fmt.Errorf("services.%s.kind must be one of %q, %q, or %q", name, ServiceKindWeb, ServiceKindWorker, ServiceKindAccessory)
	}
	for _, arg := range service.Command {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("services.%s.command entries must be present", name)
		}
	}
	for _, arg := range service.Args {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("services.%s.args entries must be present", name)
		}
	}
	for key := range service.Env {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("services.%s.env keys must be present", name)
		}
	}
	for _, secret := range service.SecretRefs {
		if strings.TrimSpace(secret.Name) == "" {
			return fmt.Errorf("services.%s.secret_refs[].name is required", name)
		}
		if strings.TrimSpace(secret.Secret) == "" {
			return fmt.Errorf("services.%s.secret_refs[].secret is required", name)
		}
	}
	for _, volume := range service.Volumes {
		if strings.TrimSpace(volume.Source) == "" {
			return fmt.Errorf("services.%s.volumes[].source is required", name)
		}
		if strings.TrimSpace(volume.Target) == "" {
			return fmt.Errorf("services.%s.volumes[].target is required", name)
		}
		if !filepath.IsAbs(volume.Target) {
			return fmt.Errorf("services.%s.volumes[].target must be absolute", name)
		}
	}
	seenPorts := map[string]bool{}
	for _, port := range service.Ports {
		if strings.TrimSpace(port.Name) == "" {
			return fmt.Errorf("services.%s.ports[].name is required", name)
		}
		if port.Port <= 0 {
			return fmt.Errorf("services.%s.ports[%s].port must be a positive integer", name, port.Name)
		}
		if seenPorts[port.Name] {
			return fmt.Errorf("services.%s.ports contains duplicate port %q", name, port.Name)
		}
		seenPorts[port.Name] = true
	}
	if service.Kind == ServiceKindWeb {
		if service.HTTPPort(0) <= 0 {
			return fmt.Errorf("services.%s must expose an http port", name)
		}
		if service.Healthcheck == nil {
			return fmt.Errorf("services.%s.healthcheck is required", name)
		}
		if strings.TrimSpace(service.Healthcheck.Path) == "" {
			return fmt.Errorf("services.%s.healthcheck.path is required", name)
		}
		if service.Healthcheck.Port <= 0 {
			return fmt.Errorf("services.%s.healthcheck.port must be a positive integer", name)
		}
	}
	return nil
}

func validateTasks(cfg *ProjectConfig) error {
	release := cfg.Tasks.Release
	if release == nil {
		return nil
	}
	serviceName := strings.TrimSpace(release.Service)
	if serviceName == "" {
		return errors.New("tasks.release.service is required")
	}
	if _, ok := cfg.Services[serviceName]; !ok {
		return fmt.Errorf("tasks.release.service %q not found in services", serviceName)
	}
	if len(release.Command) == 0 && len(release.Args) == 0 {
		return errors.New("tasks.release must set command or args")
	}
	for _, arg := range release.Command {
		if strings.TrimSpace(arg) == "" {
			return errors.New("tasks.release.command entries must be present")
		}
	}
	for _, arg := range release.Args {
		if strings.TrimSpace(arg) == "" {
			return errors.New("tasks.release.args entries must be present")
		}
	}
	for key := range release.Env {
		if strings.TrimSpace(key) == "" {
			return errors.New("tasks.release.env keys must be present")
		}
	}
	return nil
}

func (cfg ProjectConfig) ServiceNames() []string {
	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (cfg ProjectConfig) ServicesByKind(kind string) []string {
	names := []string{}
	for _, name := range cfg.ServiceNames() {
		if cfg.Services[name].Kind == kind {
			names = append(names, name)
		}
	}
	return names
}

func (cfg ProjectConfig) PrimaryWebServiceName() (string, bool) {
	services := cfg.ServicesByKind(ServiceKindWeb)
	if len(services) == 0 {
		return "", false
	}
	if len(services) == 1 {
		return services[0], true
	}
	if _, ok := cfg.Services[DefaultWebServiceName]; ok && cfg.Services[DefaultWebServiceName].Kind == ServiceKindWeb {
		return DefaultWebServiceName, true
	}
	return "", false
}

func (cfg ProjectConfig) ReleaseTask() *TaskConfig {
	return cfg.Tasks.Release
}

func (service ServiceConfig) HTTPPort(fallback int) int {
	for _, port := range service.Ports {
		if strings.TrimSpace(port.Name) == "http" && port.Port > 0 {
			return port.Port
		}
	}
	return fallback
}

func mergeStringMaps(parts ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, part := range parts {
		for key, value := range part {
			merged[key] = value
		}
	}
	return merged
}

func validateEnvironmentOverlays(cfg *ProjectConfig) error {
	for envName, overlay := range cfg.Environments {
		name := strings.TrimSpace(envName)
		if name == "" {
			return errors.New("environments keys must be present")
		}
		if err := validateEnvironmentOverlay(name, overlay, cfg); err != nil {
			return err
		}
		resolved, err := ResolveEnvironmentConfig(*cfg, name)
		if err != nil {
			return err
		}
		if err := validateResolvedProjectConfig(&resolved); err != nil {
			return fmt.Errorf("environments.%s: %w", name, err)
		}
	}
	return nil
}

func validateEnvironmentOverlay(envName string, overlay EnvironmentOverlay, cfg *ProjectConfig) error {
	if overlay.Ingress != nil {
		if overlay.Ingress.Service != nil && strings.TrimSpace(*overlay.Ingress.Service) == "" {
			return fmt.Errorf("environments.%s.ingress.service must be present", envName)
		}
		if overlay.Ingress.TLS != nil {
			for field, value := range map[string]*string{
				"mode":             overlay.Ingress.TLS.Mode,
				"email":            overlay.Ingress.TLS.Email,
				"ca_directory_url": overlay.Ingress.TLS.CADirectoryURL,
			} {
				if value != nil && strings.TrimSpace(*value) == "" {
					return fmt.Errorf("environments.%s.ingress.tls.%s must be present", envName, field)
				}
			}
		}
	}
	for serviceName, service := range overlay.Services {
		if _, ok := cfg.Services[serviceName]; !ok {
			return fmt.Errorf("environments.%s.services.%s not found in services", envName, serviceName)
		}
		for field, value := range map[string]*string{
			"kind":  service.Kind,
			"image": service.Image,
		} {
			if value != nil && strings.TrimSpace(*value) == "" {
				return fmt.Errorf("environments.%s.services.%s.%s must be present", envName, serviceName, field)
			}
		}
		if service.Healthcheck != nil && service.Healthcheck.Port != nil && *service.Healthcheck.Port <= 0 {
			return fmt.Errorf("environments.%s.services.%s.healthcheck.port must be a positive integer", envName, serviceName)
		}
	}
	if overlay.Tasks != nil && overlay.Tasks.Release != nil {
		release := overlay.Tasks.Release
		if release.Service != nil && strings.TrimSpace(*release.Service) == "" {
			return fmt.Errorf("environments.%s.tasks.release.service must be present", envName)
		}
	}
	return nil
}

func cloneProjectConfig(cfg ProjectConfig) ProjectConfig {
	cloned := cfg
	cloned.Build.Platforms = append([]string(nil), cfg.Build.Platforms...)
	cloned.Services = map[string]ServiceConfig{}
	for name, service := range cfg.Services {
		cloned.Services[name] = cloneServiceConfig(service)
	}
	if cfg.Tasks.Release != nil {
		release := *cfg.Tasks.Release
		release.Command = append([]string(nil), cfg.Tasks.Release.Command...)
		release.Args = append([]string(nil), cfg.Tasks.Release.Args...)
		release.Env = cloneStringMap(cfg.Tasks.Release.Env)
		cloned.Tasks.Release = &release
	}
	if cfg.Ingress != nil {
		ingress := *cfg.Ingress
		ingress.Hosts = append([]string(nil), cfg.Ingress.Hosts...)
		cloned.Ingress = &ingress
	}
	cloned.Environments = cfg.Environments
	return cloned
}

func cloneServiceConfig(service ServiceConfig) ServiceConfig {
	cloned := service
	cloned.Command = append([]string(nil), service.Command...)
	cloned.Args = append([]string(nil), service.Args...)
	cloned.Env = cloneStringMap(service.Env)
	cloned.SecretRefs = append([]SecretRef(nil), service.SecretRefs...)
	cloned.Ports = append([]ServicePort(nil), service.Ports...)
	cloned.Volumes = append([]Volume(nil), service.Volumes...)
	if service.Healthcheck != nil {
		healthcheck := *service.Healthcheck
		cloned.Healthcheck = &healthcheck
	}
	return cloned
}

func mergeServiceConfig(base ServiceConfig, overlay ServiceConfigOverlay) ServiceConfig {
	merged := cloneServiceConfig(base)
	if overlay.Kind != nil {
		merged.Kind = strings.TrimSpace(*overlay.Kind)
	}
	if overlay.Image != nil {
		merged.Image = strings.TrimSpace(*overlay.Image)
	}
	if overlay.Command != nil {
		merged.Command = append([]string(nil), overlay.Command...)
	}
	if overlay.Args != nil {
		merged.Args = append([]string(nil), overlay.Args...)
	}
	if overlay.Env != nil {
		merged.Env = mergeStringMaps(merged.Env, overlay.Env)
	}
	if overlay.SecretRefs != nil {
		merged.SecretRefs = append([]SecretRef(nil), overlay.SecretRefs...)
	}
	if overlay.Ports != nil {
		merged.Ports = append([]ServicePort(nil), overlay.Ports...)
	}
	if overlay.Volumes != nil {
		merged.Volumes = append([]Volume(nil), overlay.Volumes...)
	}
	if overlay.Healthcheck != nil {
		if merged.Healthcheck == nil {
			merged.Healthcheck = &HTTPHealthcheck{}
		}
		if overlay.Healthcheck.Path != nil {
			merged.Healthcheck.Path = strings.TrimSpace(*overlay.Healthcheck.Path)
		}
		if overlay.Healthcheck.Port != nil {
			merged.Healthcheck.Port = *overlay.Healthcheck.Port
		}
	}
	return merged
}

func mergeTasksConfig(base TasksConfig, overlay *TasksConfigOverlay) TasksConfig {
	merged := base
	if overlay == nil || overlay.Release == nil {
		return merged
	}
	if merged.Release == nil {
		merged.Release = &TaskConfig{}
	}
	if overlay.Release.Service != nil {
		merged.Release.Service = strings.TrimSpace(*overlay.Release.Service)
	}
	if overlay.Release.Command != nil {
		merged.Release.Command = append([]string(nil), overlay.Release.Command...)
	}
	if overlay.Release.Args != nil {
		merged.Release.Args = append([]string(nil), overlay.Release.Args...)
	}
	if overlay.Release.Env != nil {
		merged.Release.Env = mergeStringMaps(merged.Release.Env, overlay.Release.Env)
	}
	return merged
}

func mergeIngressConfig(base *IngressConfig, overlay *IngressConfigOverlay) *IngressConfig {
	if overlay == nil {
		if base == nil {
			return nil
		}
		cloned := *base
		cloned.Hosts = append([]string(nil), base.Hosts...)
		return &cloned
	}
	merged := &IngressConfig{}
	if base != nil {
		*merged = *base
		merged.Hosts = append([]string(nil), base.Hosts...)
	}
	if overlay.Hosts != nil {
		merged.Hosts = append([]string(nil), overlay.Hosts...)
	}
	if overlay.Service != nil {
		merged.Service = strings.TrimSpace(*overlay.Service)
	}
	if overlay.TLS != nil {
		if overlay.TLS.Mode != nil {
			merged.TLS.Mode = strings.TrimSpace(*overlay.TLS.Mode)
		}
		if overlay.TLS.Email != nil {
			merged.TLS.Email = strings.TrimSpace(*overlay.TLS.Email)
		}
		if overlay.TLS.CADirectoryURL != nil {
			merged.TLS.CADirectoryURL = strings.TrimSpace(*overlay.TLS.CADirectoryURL)
		}
	}
	if overlay.RedirectHTTP != nil {
		merged.RedirectHTTP = *overlay.RedirectHTTP
	}
	return merged
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := map[string]string{}
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
