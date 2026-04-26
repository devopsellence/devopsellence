package config

import core "github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"

const (
	FilePath               = core.FilePath
	GenericFilePath        = core.GenericFilePath
	SchemaVersion          = core.SchemaVersion
	DefaultEnvironment     = core.DefaultEnvironment
	DefaultBuildContext    = core.DefaultBuildContext
	DefaultDockerfile      = core.DefaultDockerfile
	DefaultHealthcheckPath = core.DefaultHealthcheckPath
	DefaultWebPort         = core.DefaultWebPort
	AppTypeRails           = core.AppTypeRails
	AppTypeGeneric         = core.AppTypeGeneric
	DefaultWebRole         = core.DefaultWebRole
	DefaultWorkerRole      = core.DefaultWorkerRole
	DefaultWebServiceName  = core.DefaultWebServiceName
	ServiceKindWeb         = core.ServiceKindWeb
	ServiceKindWorker      = core.ServiceKindWorker
	ServiceKindAccessory   = core.ServiceKindAccessory
)

var DefaultBuildPlatforms = core.DefaultBuildPlatforms
var SoloDefaultLabels = core.SoloDefaultLabels

type Volume = core.Volume
type SecretRef = core.SecretRef
type HTTPHealthcheck = core.HTTPHealthcheck
type ServicePort = core.ServicePort
type ServiceConfig = core.ServiceConfig
type Service = core.Service
type TaskConfig = core.TaskConfig
type TasksConfig = core.TasksConfig
type BuildConfig = core.BuildConfig
type AppConfig = core.AppConfig
type IngressTLSConfig = core.IngressTLSConfig
type IngressConfig = core.IngressConfig
type IngressRuleConfig = core.IngressRuleConfig
type IngressMatchConfig = core.IngressMatchConfig
type IngressTargetConfig = core.IngressTargetConfig
type HTTPHealthcheckOverlay = core.HTTPHealthcheckOverlay
type ServiceConfigOverlay = core.ServiceConfigOverlay
type TaskConfigOverlay = core.TaskConfigOverlay
type TasksConfigOverlay = core.TasksConfigOverlay
type IngressTLSConfigOverlay = core.IngressTLSConfigOverlay
type IngressConfigOverlay = core.IngressConfigOverlay
type EnvironmentOverlay = core.EnvironmentOverlay
type SoloNode = core.SoloNode
type ProjectConfig = core.ProjectConfig
type Project = core.Project
type Store = core.Store

func NewStore() Store                                  { return core.NewStore() }
func Load(path string) (*ProjectConfig, error)         { return core.Load(path) }
func ExistingPath(workspaceRoot string) (string, bool) { return core.ExistingPath(workspaceRoot) }
func PathForType(workspaceRoot, appType string) string {
	return core.PathForType(workspaceRoot, appType)
}
func LoadFromRoot(workspaceRoot string) (*ProjectConfig, error) {
	return core.LoadFromRoot(workspaceRoot)
}
func Write(workspaceRoot string, cfg ProjectConfig) (ProjectConfig, error) {
	return core.Write(workspaceRoot, cfg)
}
func DefaultProjectConfig(organization, project, environment string) ProjectConfig {
	return core.DefaultProjectConfig(organization, project, environment)
}
func DefaultProjectConfigForType(organization, project, environment, appType string) ProjectConfig {
	return core.DefaultProjectConfigForType(organization, project, environment, appType)
}
func Validate(cfg *ProjectConfig) error { return core.Validate(cfg) }
func ResolveEnvironmentConfig(cfg ProjectConfig, environment string) (ProjectConfig, error) {
	return core.ResolveEnvironmentConfig(cfg, environment)
}
func InferredServiceKind(name string, service ServiceConfig) string {
	return core.InferredServiceKind(name, service)
}
