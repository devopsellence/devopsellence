package solo

import (
	"github.com/devopsellence/cli/internal/config"
	core "github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/desiredstate"
)

// Desired-state JSON types matching the agent protobuf schema (camelCase keys).
// The implementation lives in deployment-core; these aliases preserve existing
// solo package call sites while the shared core becomes the authority.
type desiredStateJSON = core.DesiredStateJSON
type environmentJSON = core.EnvironmentJSON
type serviceJSON = core.ServiceJSON
type servicePortJSON = core.ServicePortJSON
type healthcheckJSON = core.HealthcheckJSON
type volumeMountJSON = core.VolumeMountJSON
type taskJSON = core.TaskJSON
type ingressJSON = core.IngressJSON
type ingressTLSJSON = core.IngressTLSJSON
type ingressRouteJSON = core.IngressRouteJSON
type ingressMatchJSON = core.IngressMatchJSON
type ingressTargetJSON = core.IngressTargetJSON
type nodePeerJSON = core.NodePeerJSON
type NodePeer = core.NodePeer

func BuildDesiredState(cfg *config.ProjectConfig, imageTag, revision string, secrets map[string]string) ([]byte, error) {
	return core.BuildDesiredState(cfg, imageTag, revision, secrets)
}

func BuildDesiredStateWithScopedSecrets(cfg *config.ProjectConfig, imageTag, revision string, secrets ScopedSecrets) ([]byte, error) {
	return core.BuildDesiredStateWithScopedSecrets(cfg, imageTag, revision, core.ScopedSecrets(secrets))
}

func BuildDesiredStateForLabels(cfg *config.ProjectConfig, imageTag, revision string, secrets map[string]string, labels []string, includeReleaseTask bool) ([]byte, error) {
	return core.BuildDesiredStateForLabels(cfg, imageTag, revision, secrets, labels, includeReleaseTask)
}

func BuildDesiredStateForNode(cfg *config.ProjectConfig, imageTag, revision string, secrets map[string]string, labels []string, ingressNode bool, includeReleaseTask bool, nodePeers ...[]NodePeer) ([]byte, error) {
	return core.BuildDesiredStateForNode(cfg, imageTag, revision, secrets, labels, ingressNode, includeReleaseTask, nodePeers...)
}

func BuildDesiredStateForNodeWithScopedSecrets(cfg *config.ProjectConfig, imageTag, revision string, secrets ScopedSecrets, labels []string, ingressNode bool, includeReleaseTask bool, nodePeers ...[]NodePeer) ([]byte, error) {
	return core.BuildDesiredStateForNodeWithScopedSecrets(cfg, imageTag, revision, core.ScopedSecrets(secrets), labels, ingressNode, includeReleaseTask, nodePeers...)
}

func BuildAggregatedDesiredState(nodeName string, currentNode config.SoloNode, snapshots []DeploySnapshot, releaseNodes map[string]string, nodePeers []NodePeer) ([]byte, error) {
	publication, err := core.PlanNodePublication(core.NodePublicationInput{
		NodeName:     nodeName,
		CurrentNode:  currentNode,
		Snapshots:    snapshots,
		ReleaseNodes: releaseNodes,
		NodePeers:    nodePeers,
	})
	if err != nil {
		return nil, err
	}
	return publication.DesiredStateJSON, nil
}

func buildService(serviceName string, svc config.ServiceConfig, imageTag string, secrets map[string]string) (serviceJSON, error) {
	return core.BuildService(serviceName, svc, imageTag, secrets)
}

func buildReleaseTask(cfg *config.ProjectConfig, imageTag string, secrets map[string]string) (taskJSON, error) {
	return core.BuildReleaseTask(cfg, imageTag, secrets)
}

func buildIngress(ingress *config.IngressConfig, environmentName string) *ingressJSON {
	return core.BuildIngress(ingress, environmentName)
}

func mergeIngressForNode(labels []string, snapshots []DeploySnapshot, environmentNames map[string]string) (*ingressJSON, error) {
	return core.MergeIngressForNode(labels, snapshots, environmentNames)
}

func aggregatedEnvironmentNames(snapshots []DeploySnapshot) map[string]string {
	return core.AggregatedEnvironmentNames(snapshots)
}
