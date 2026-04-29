package desiredstate

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
)

func boolPtr(value bool) *bool {
	return &value
}

func baseProject() *config.ProjectConfig {
	cfg := config.DefaultProjectConfig("solo", "myapp", config.DefaultEnvironment)
	cfg.Services["web"] = config.ServiceConfig{
		Command: []string{"./bin/server"},
		Env:     map[string]string{"APP_ENV": "production"},
		SecretRefs: []config.SecretRef{
			{Name: "DATABASE_URL", Secret: "projects/x/secrets/db"},
		},
		Ports:       []config.ServicePort{{Name: "http", Port: 3000}},
		Healthcheck: &config.HTTPHealthcheck{Path: "/up", Port: 3000},
	}
	return &cfg
}

func TestBuildDesiredState_WebOnly(t *testing.T) {
	cfg := baseProject()
	secrets := map[string]string{"DATABASE_URL": "postgres://localhost/mydb"}

	data, err := BuildDesiredState(cfg, "myapp:abc1234", "abc1234", secrets)
	if err != nil {
		t.Fatal(err)
	}

	var ds DesiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}

	if ds.Revision != "abc1234" {
		t.Errorf("expected revision abc1234, got %s", ds.Revision)
	}
	environment := ds.Environments[0]
	if environment.Name != config.DefaultEnvironment {
		t.Fatalf("expected default environment, got %s", environment.Name)
	}
	if len(environment.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(environment.Services))
	}

	web := environment.Services[0]
	if web.Name != "web" || web.Kind != "web" || web.Image != "myapp:abc1234" {
		t.Fatalf("web service = %#v", web)
	}
	if web.Env["APP_ENV"] != "production" || web.Env["DATABASE_URL"] != "postgres://localhost/mydb" {
		t.Fatalf("env = %#v", web.Env)
	}
	if web.Healthcheck == nil || web.Healthcheck.Path != "/up" {
		t.Fatalf("healthcheck = %#v", web.Healthcheck)
	}
	if len(web.Ports) != 1 || web.Ports[0].Name != "http" || web.Ports[0].Port != 3000 {
		t.Fatalf("ports = %#v", web.Ports)
	}
	if got, want := web.Entrypoint, []string{"./bin/server"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entrypoint = %#v, want %#v", got, want)
	}
}

func TestBuildDesiredStateWithScopedSecretsUsesServiceScope(t *testing.T) {
	cfg := baseProject()
	cfg.Services["worker"] = config.ServiceConfig{
		Command:    []string{"bundle", "exec", "sidekiq"},
		SecretRefs: []config.SecretRef{{Name: "DATABASE_URL", Secret: "local"}},
	}
	secrets := ScopedSecrets{
		"web":    {"DATABASE_URL": "postgres://web"},
		"worker": {"DATABASE_URL": "postgres://worker"},
	}

	data, err := BuildDesiredStateWithScopedSecrets(cfg, "myapp:abc1234", "abc1234", secrets)
	if err != nil {
		t.Fatal(err)
	}

	var ds DesiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}
	envByService := map[string]map[string]string{}
	for _, service := range ds.Environments[0].Services {
		envByService[service.Name] = service.Env
	}
	if got := envByService["web"]["DATABASE_URL"]; got != "postgres://web" {
		t.Fatalf("web DATABASE_URL = %q", got)
	}
	if got := envByService["worker"]["DATABASE_URL"]; got != "postgres://worker" {
		t.Fatalf("worker DATABASE_URL = %q", got)
	}
}

func TestBuildDesiredState_WithNamedWorkerAndReleaseTask(t *testing.T) {
	cfg := baseProject()
	cfg.Services["jobs"] = config.ServiceConfig{
		Command: []string{"sidekiq"},
		Env:     map[string]string{"QUEUE": "default"},
	}
	cfg.Tasks.Release = &config.TaskConfig{Service: "web", Command: []string{"./bin/migrate"}}

	data, err := BuildDesiredState(cfg, "myapp:def5678", "def5678", map[string]string{"DATABASE_URL": "postgres://localhost/mydb"})
	if err != nil {
		t.Fatal(err)
	}

	var ds DesiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}

	environment := ds.Environments[0]
	if len(environment.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(environment.Services))
	}
	if environment.Services[0].Name != "jobs" || environment.Services[1].Name != "web" {
		t.Fatalf("services = %#v", environment.Services)
	}
	if len(environment.Tasks) != 1 {
		t.Fatal("expected release task")
	}
	releaseTask := environment.Tasks[0]
	if releaseTask.Name != "release" || releaseTask.Image != "myapp:def5678" {
		t.Fatalf("release task = %#v", releaseTask)
	}
}

func TestBuildDesiredState_MapsArgsToContainerCommand(t *testing.T) {
	cfg := baseProject()
	cfg.Services["web"] = config.ServiceConfig{
		Command:     []string{"/app"},
		Args:        []string{"web", "--port", "3000"},
		Ports:       []config.ServicePort{{Name: "http", Port: 3000}},
		Healthcheck: &config.HTTPHealthcheck{Path: "/up", Port: 3000},
	}
	cfg.Tasks.Release = &config.TaskConfig{Service: "web", Args: []string{"release"}}

	data, err := BuildDesiredState(cfg, "myapp:def5678", "def5678", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	var ds DesiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}
	service := ds.Environments[0].Services[0]
	if got, want := service.Entrypoint, []string{"/app"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entrypoint = %#v, want %#v", got, want)
	}
	if got, want := service.Command, []string{"web", "--port", "3000"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("command = %#v, want %#v", got, want)
	}
	task := ds.Environments[0].Tasks[0]
	if got, want := task.Entrypoint, []string{"/app"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("task entrypoint = %#v, want %#v", got, want)
	}
	if got, want := task.Command, []string{"release"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("task command = %#v, want %#v", got, want)
	}
}

func TestBuildDesiredStateForLabelsFiltersServicesByKindLabel(t *testing.T) {
	cfg := baseProject()
	cfg.Services["jobs"] = config.ServiceConfig{
		Command: []string{"sidekiq"},
	}
	cfg.Tasks.Release = &config.TaskConfig{Service: "web", Command: []string{"./bin/migrate"}}

	data, err := BuildDesiredStateForLabels(cfg, "myapp:def5678", "def5678", map[string]string{"DATABASE_URL": "postgres://localhost/mydb"}, []string{config.DefaultWorkerRole}, false)
	if err != nil {
		t.Fatal(err)
	}
	var ds DesiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}
	environment := ds.Environments[0]
	if len(environment.Services) != 1 || environment.Services[0].Name != "jobs" {
		t.Fatalf("services = %#v, want jobs only", environment.Services)
	}
	if len(environment.Tasks) != 0 {
		t.Fatal("release task should not be scheduled on worker-only node")
	}
}

func TestBuildDesiredStateForLabelsIncludesReleaseWhenSelected(t *testing.T) {
	cfg := baseProject()
	cfg.Tasks.Release = &config.TaskConfig{Service: "web", Command: []string{"./bin/migrate"}}

	data, err := BuildDesiredStateForLabels(cfg, "myapp:def5678", "def5678", map[string]string{"DATABASE_URL": "postgres://localhost/mydb"}, []string{config.DefaultWebRole}, true)
	if err != nil {
		t.Fatal(err)
	}
	var ds DesiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}
	environment := ds.Environments[0]
	if len(environment.Services) != 1 || environment.Services[0].Name != "web" {
		t.Fatalf("services = %#v, want web only", environment.Services)
	}
	if len(environment.Tasks) != 1 {
		t.Fatal("expected release task")
	}
}

func TestBuildDesiredStateForNodeIncludesIngressForIngressNode(t *testing.T) {
	cfg := baseProject()
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"app.example.com", "www.example.com"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "app.example.com", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: "web", Port: "http"},
		}, {
			Match:  config.IngressMatchConfig{Host: "www.example.com", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: "web", Port: "http"},
		}},
		TLS: config.IngressTLSConfig{
			Mode:           "auto",
			Email:          "ops@example.com",
			CADirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory",
		},
		RedirectHTTP: boolPtr(true),
	}

	data, err := BuildDesiredStateForNode(cfg, "myapp:def5678", "def5678", map[string]string{"DATABASE_URL": "postgres://localhost/mydb"}, []string{config.DefaultWebRole}, true, false, []NodePeer{{
		Name:          "web-b",
		Labels:        []string{config.DefaultWebRole},
		PublicAddress: "203.0.113.11",
	}})
	if err != nil {
		t.Fatal(err)
	}
	var ds DesiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}
	if ds.Ingress == nil {
		t.Fatal("expected ingress")
	}
	if ds.Ingress.Mode != "public" {
		t.Fatalf("mode = %q, want public", ds.Ingress.Mode)
	}
	if strings.Join(ds.Ingress.Hosts, ",") != "app.example.com,www.example.com" {
		t.Fatalf("hosts = %#v", ds.Ingress.Hosts)
	}
	if len(ds.Ingress.Routes) != 2 || ds.Ingress.Routes[0].Target.Service != "web" {
		t.Fatalf("routes = %#v", ds.Ingress.Routes)
	}
	if len(ds.NodePeers) != 1 || ds.NodePeers[0].Name != "web-b" {
		t.Fatalf("node peers = %#v", ds.NodePeers)
	}
}

func TestBuildDesiredStateForNodeOmitsIngressForNonIngressNode(t *testing.T) {
	cfg := baseProject()
	cfg.Services["jobs"] = config.ServiceConfig{
		Command: []string{"sidekiq"},
	}
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"app.example.com"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "app.example.com", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: "web", Port: "http"},
		}},
	}

	data, err := BuildDesiredStateForNode(cfg, "myapp:def5678", "def5678", map[string]string{"DATABASE_URL": "postgres://localhost/mydb"}, []string{config.DefaultWorkerRole}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	var ds DesiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}
	if ds.Ingress != nil {
		t.Fatalf("ingress = %#v, want nil", ds.Ingress)
	}
}

func TestBuildDesiredStateForNodeDefaultsIngressRedirectHTTPToTrue(t *testing.T) {
	cfg := baseProject()
	cfg.Ingress = &config.IngressConfig{
		Hosts: []string{"app.example.com"},
		Rules: []config.IngressRuleConfig{{
			Match:  config.IngressMatchConfig{Host: "app.example.com", PathPrefix: "/"},
			Target: config.IngressTargetConfig{Service: "web", Port: "http"},
		}},
	}

	data, err := BuildDesiredStateForNode(cfg, "myapp:def5678", "def5678", map[string]string{"DATABASE_URL": "postgres://localhost/mydb"}, []string{config.DefaultWebRole}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	var ds DesiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}
	if ds.Ingress == nil {
		t.Fatal("expected ingress")
	}
	if !ds.Ingress.RedirectHTTP {
		t.Fatalf("redirect_http = %v, want true", ds.Ingress.RedirectHTTP)
	}
}

func TestBuildDesiredState_MissingSecretErrors(t *testing.T) {
	cfg := baseProject()
	_, err := BuildDesiredState(cfg, "myapp:abc1234", "abc1234", map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing secret, got nil")
	}
	if got := err.Error(); !strings.Contains(got, "DATABASE_URL") {
		t.Errorf("expected error to mention DATABASE_URL, got: %s", got)
	}
}

func TestPlanNodePublicationMergesEnvironmentsIngressAndPeers(t *testing.T) {
	webNode := config.Node{Labels: []string{config.DefaultWebRole}}
	snapshots := []DeploySnapshot{
		{
			WorkspaceRoot:      "/workspace/a",
			WorkspaceKey:       "/workspace/a",
			Environment:        "production",
			Revision:           "aaa1111",
			Image:              "demo-a:aaa1111",
			Services:           []ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb, Image: "demo-a:aaa1111"}},
			ReleaseTask:        &TaskJSON{Name: "release", Image: "demo-a:aaa1111"},
			ReleaseService:     "web",
			ReleaseServiceKind: config.ServiceKindWeb,
			Ingress: &IngressJSON{
				Mode:         "public",
				Hosts:        []string{"a.example.com"},
				TLS:          IngressTLSJSON{Mode: "auto"},
				RedirectHTTP: true,
				Routes: []IngressRouteJSON{{
					Match:  IngressMatchJSON{Hostname: "a.example.com"},
					Target: IngressTargetJSON{Environment: "production", Service: "web", Port: "http"},
				}},
			},
			IngressService:     "web",
			IngressServiceKind: config.ServiceKindWeb,
		},
		{
			WorkspaceRoot:      "/workspace/b",
			WorkspaceKey:       "/workspace/b",
			Environment:        "production",
			Revision:           "bbb2222",
			Image:              "demo-b:bbb2222",
			Services:           []ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb, Image: "demo-b:bbb2222"}},
			ReleaseService:     "web",
			ReleaseServiceKind: config.ServiceKindWeb,
			Ingress: &IngressJSON{
				Mode:         "public",
				Hosts:        []string{"b.example.com"},
				TLS:          IngressTLSJSON{Mode: "auto"},
				RedirectHTTP: true,
				Routes: []IngressRouteJSON{{
					Match:  IngressMatchJSON{Hostname: "b.example.com"},
					Target: IngressTargetJSON{Environment: "production", Service: "web", Port: "http"},
				}},
			},
			IngressService:     "web",
			IngressServiceKind: config.ServiceKindWeb,
		},
	}
	releaseNodes := map[string]string{
		"/workspace/a\nproduction": "shared-1",
	}
	peers := []NodePeer{
		{Name: "shared-2", Labels: []string{config.DefaultWebRole}, PublicAddress: "203.0.113.12"},
		{Name: "shared-3", Labels: []string{config.DefaultWorkerRole}, PublicAddress: "203.0.113.13"},
	}

	firstPublication, err := PlanNodePublication(NodePublicationInput{NodeName: "shared-1", CurrentNode: webNode, Snapshots: snapshots, ReleaseNodes: releaseNodes, NodePeers: peers})
	first := firstPublication.DesiredStateJSON
	if err != nil {
		t.Fatal(err)
	}
	secondPublication, err := PlanNodePublication(NodePublicationInput{NodeName: "shared-1", CurrentNode: webNode, Snapshots: snapshots, ReleaseNodes: releaseNodes, NodePeers: peers})
	second := secondPublication.DesiredStateJSON
	if err != nil {
		t.Fatal(err)
	}

	var ds DesiredStateJSON
	if err := json.Unmarshal(first, &ds); err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("expected deterministic aggregated desired state output")
	}
	if len(ds.Environments) != 2 || ds.Environments[0].Revision != "aaa1111" || ds.Environments[1].Revision != "bbb2222" {
		t.Fatalf("environments = %#v", ds.Environments)
	}
	if ds.Ingress == nil || len(ds.Ingress.Routes) != 2 {
		t.Fatalf("ingress = %#v", ds.Ingress)
	}
	if strings.Join(ds.Ingress.Hosts, ",") != "a.example.com,b.example.com" {
		t.Fatalf("hosts = %#v", ds.Ingress.Hosts)
	}
	if len(ds.NodePeers) != 2 || ds.NodePeers[0].Name != "shared-2" || ds.NodePeers[1].Name != "shared-3" {
		t.Fatalf("node peers = %#v", ds.NodePeers)
	}
	if ds.Revision == "" {
		t.Fatal("synthetic revision empty")
	}
	if len(ds.Environments[0].Tasks) != 1 || len(ds.Environments[1].Tasks) != 0 {
		t.Fatalf("tasks = %#v", ds.Environments)
	}
}

func TestPlanNodePublicationWrapsAggregatedDesiredState(t *testing.T) {
	t.Parallel()

	currentNode := config.Node{Labels: []string{config.DefaultWebRole}}
	snapshots := []DeploySnapshot{
		{
			WorkspaceRoot:      "/workspace/demo",
			WorkspaceKey:       "/workspace/demo",
			Environment:        "production",
			Revision:           "aaa1111",
			Services:           []ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb, Image: "demo:aaa1111"}},
			ReleaseTask:        &TaskJSON{Name: "release", Image: "demo:aaa1111"},
			ReleaseServiceKind: config.ServiceKindWeb,
		},
	}

	releaseNodes := map[string]string{"/workspace/demo\nproduction": "web-a"}
	peers := []NodePeer{{Name: "web-b", Labels: []string{config.DefaultWebRole}, PublicAddress: "203.0.113.12"}}

	wantPublication, err := PlanNodePublication(NodePublicationInput{NodeName: "web-a", CurrentNode: currentNode, Snapshots: snapshots, ReleaseNodes: releaseNodes, NodePeers: peers})
	want := wantPublication.DesiredStateJSON
	if err != nil {
		t.Fatal(err)
	}
	got, err := PlanNodePublication(NodePublicationInput{
		NodeName:     "web-a",
		CurrentNode:  currentNode,
		Snapshots:    snapshots,
		ReleaseNodes: releaseNodes,
		NodePeers:    peers,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NodeName != "web-a" {
		t.Fatalf("node name = %q, want web-a", got.NodeName)
	}
	if string(got.DesiredStateJSON) != string(want) {
		t.Fatalf("desired state JSON differs\ngot:  %s\nwant: %s", got.DesiredStateJSON, want)
	}
}

func TestMergeIngressForNodeRejectsDuplicateHostPathAcrossTargets(t *testing.T) {
	t.Parallel()

	snapshots := []DeploySnapshot{
		{
			Services: []ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb}},
			Ingress: &IngressJSON{
				Mode:  "public",
				TLS:   IngressTLSJSON{Mode: "auto"},
				Hosts: []string{"app.example.com"},
				Routes: []IngressRouteJSON{{
					Match:  IngressMatchJSON{Hostname: "app.example.com", PathPrefix: "/"},
					Target: IngressTargetJSON{Environment: "production", Service: "web", Port: "metrics"},
				}},
			},
			IngressService:     "web",
			IngressServiceKind: config.ServiceKindWeb,
		},
		{
			Services: []ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb}},
			Ingress: &IngressJSON{
				Mode:  "public",
				TLS:   IngressTLSJSON{Mode: "auto"},
				Hosts: []string{"app.example.com"},
				Routes: []IngressRouteJSON{{
					Match:  IngressMatchJSON{Hostname: "app.example.com", PathPrefix: "/"},
					Target: IngressTargetJSON{Environment: "staging", Service: "web", Port: "http"},
				}},
			},
			IngressService:     "web",
			IngressServiceKind: config.ServiceKindWeb,
		},
	}

	_, err := mergeIngressForNode([]string{config.DefaultWebRole}, snapshots, aggregatedEnvironmentNames(snapshots))
	if err == nil || !strings.Contains(err.Error(), "duplicate route") {
		t.Fatalf("mergeIngressForNode() error = %v, want duplicate route", err)
	}
}

func TestMergeIngressForNodeTreatsBlankAndPublicModeAsEquivalent(t *testing.T) {
	t.Parallel()

	snapshots := []DeploySnapshot{
		{
			Services: []ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb}},
			Ingress: &IngressJSON{
				TLS:   IngressTLSJSON{Mode: "auto"},
				Hosts: []string{"a.example.com"},
				Routes: []IngressRouteJSON{{
					Match:  IngressMatchJSON{Hostname: "a.example.com"},
					Target: IngressTargetJSON{Environment: "production", Service: "web", Port: "http"},
				}},
			},
			IngressService:     "web",
			IngressServiceKind: config.ServiceKindWeb,
		},
		{
			Services: []ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb}},
			Ingress: &IngressJSON{
				Mode:  "public",
				TLS:   IngressTLSJSON{Mode: "auto"},
				Hosts: []string{"b.example.com"},
				Routes: []IngressRouteJSON{{
					Match:  IngressMatchJSON{Hostname: "b.example.com"},
					Target: IngressTargetJSON{Environment: "production", Service: "web", Port: "http"},
				}},
			},
			IngressService:     "web",
			IngressServiceKind: config.ServiceKindWeb,
		},
	}

	merged, err := mergeIngressForNode([]string{config.DefaultWebRole}, snapshots, aggregatedEnvironmentNames(snapshots))
	if err != nil {
		t.Fatal(err)
	}
	if merged == nil {
		t.Fatal("expected merged ingress")
	}
	if merged.Mode != "" {
		t.Fatalf("mode = %q, want empty", merged.Mode)
	}
	if strings.Join(merged.Hosts, ",") != "a.example.com,b.example.com" {
		t.Fatalf("hosts = %#v", merged.Hosts)
	}
}

func TestPlanNodePublicationNamespacesDuplicateEnvironmentNames(t *testing.T) {
	t.Parallel()

	currentNode := config.Node{Labels: []string{config.DefaultWebRole}}
	snapshots := []DeploySnapshot{
		{
			WorkspaceRoot: "/workspace/a",
			WorkspaceKey:  "/workspace/a",
			Environment:   "production",
			Revision:      "aaa1111",
			Metadata:      SnapshotMetadata{Project: "alpha"},
			Services:      []ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb, Image: "alpha:aaa1111"}},
			Ingress: &IngressJSON{
				Mode:  "public",
				TLS:   IngressTLSJSON{Mode: "auto"},
				Hosts: []string{"a.example.com"},
				Routes: []IngressRouteJSON{{
					Match:  IngressMatchJSON{Hostname: "a.example.com"},
					Target: IngressTargetJSON{Environment: "production", Service: "web", Port: "http"},
				}},
			},
			IngressService:     "web",
			IngressServiceKind: config.ServiceKindWeb,
		},
		{
			WorkspaceRoot: "/workspace/b",
			WorkspaceKey:  "/workspace/b",
			Environment:   "production",
			Revision:      "bbb2222",
			Metadata:      SnapshotMetadata{Project: "bravo"},
			Services:      []ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb, Image: "bravo:bbb2222"}},
			Ingress: &IngressJSON{
				Mode:  "public",
				TLS:   IngressTLSJSON{Mode: "auto"},
				Hosts: []string{"b.example.com"},
				Routes: []IngressRouteJSON{{
					Match:  IngressMatchJSON{Hostname: "b.example.com"},
					Target: IngressTargetJSON{Environment: "production", Service: "web", Port: "http"},
				}},
			},
			IngressService:     "web",
			IngressServiceKind: config.ServiceKindWeb,
		},
	}

	publication, err := PlanNodePublication(NodePublicationInput{NodeName: "web-a", CurrentNode: currentNode, Snapshots: snapshots, ReleaseNodes: map[string]string{}, NodePeers: nil})
	data := publication.DesiredStateJSON
	if err != nil {
		t.Fatal(err)
	}
	var ds DesiredStateJSON
	if err := json.Unmarshal(data, &ds); err != nil {
		t.Fatal(err)
	}
	if len(ds.Environments) != 2 {
		t.Fatalf("environments = %#v", ds.Environments)
	}
	gotNames := []string{ds.Environments[0].Name, ds.Environments[1].Name}
	if gotNames[0] == gotNames[1] {
		t.Fatalf("environment names should be unique: %#v", gotNames)
	}
	gotTargets := []string{ds.Ingress.Routes[0].Target.Environment, ds.Ingress.Routes[1].Target.Environment}
	sort.Strings(gotNames)
	sort.Strings(gotTargets)
	if !reflect.DeepEqual(gotNames, gotTargets) {
		t.Fatalf("ingress targets = %#v, want %#v", gotTargets, gotNames)
	}
}

func TestPlanNodePublicationKeepsEnvironmentNameStableWhenCohostedPeerDetaches(t *testing.T) {
	t.Parallel()

	currentNode := config.Node{Labels: []string{config.DefaultWebRole}}
	alpha := DeploySnapshot{
		WorkspaceRoot: "/workspace/a",
		WorkspaceKey:  "/workspace/a",
		Environment:   "production",
		Revision:      "aaa1111",
		Metadata:      SnapshotMetadata{Project: "alpha"},
		Services:      []ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb, Image: "alpha:aaa1111"}},
		Ingress: &IngressJSON{
			Mode:  "public",
			TLS:   IngressTLSJSON{Mode: "auto"},
			Hosts: []string{"a.example.com"},
			Routes: []IngressRouteJSON{{
				Match:  IngressMatchJSON{Hostname: "a.example.com"},
				Target: IngressTargetJSON{Environment: "production", Service: "web", Port: "http"},
			}},
		},
		IngressService:     "web",
		IngressServiceKind: config.ServiceKindWeb,
	}
	bravo := DeploySnapshot{
		WorkspaceRoot: "/workspace/b",
		WorkspaceKey:  "/workspace/b",
		Environment:   "production",
		Revision:      "bbb2222",
		Metadata:      SnapshotMetadata{Project: "bravo"},
		Services:      []ServiceJSON{{Name: "web", Kind: config.ServiceKindWeb, Image: "bravo:bbb2222"}},
		Ingress: &IngressJSON{
			Mode:  "public",
			TLS:   IngressTLSJSON{Mode: "auto"},
			Hosts: []string{"b.example.com"},
			Routes: []IngressRouteJSON{{
				Match:  IngressMatchJSON{Hostname: "b.example.com"},
				Target: IngressTargetJSON{Environment: "production", Service: "web", Port: "http"},
			}},
		},
		IngressService:     "web",
		IngressServiceKind: config.ServiceKindWeb,
	}

	cohosted, err := PlanNodePublication(NodePublicationInput{NodeName: "web-a", CurrentNode: currentNode, Snapshots: []DeploySnapshot{alpha, bravo}})
	if err != nil {
		t.Fatal(err)
	}
	solo, err := PlanNodePublication(NodePublicationInput{NodeName: "web-a", CurrentNode: currentNode, Snapshots: []DeploySnapshot{alpha}})
	if err != nil {
		t.Fatal(err)
	}

	var cohostedDS DesiredStateJSON
	if err := json.Unmarshal(cohosted.DesiredStateJSON, &cohostedDS); err != nil {
		t.Fatal(err)
	}
	var soloDS DesiredStateJSON
	if err := json.Unmarshal(solo.DesiredStateJSON, &soloDS); err != nil {
		t.Fatal(err)
	}
	cohostedName := ""
	for _, env := range cohostedDS.Environments {
		if env.Revision == "aaa1111" {
			cohostedName = env.Name
		}
	}
	if cohostedName == "" {
		t.Fatalf("alpha environment missing from cohosted desired state: %#v", cohostedDS.Environments)
	}
	if len(soloDS.Environments) != 1 {
		t.Fatalf("single-project desired state environments = %#v", soloDS.Environments)
	}
	if soloDS.Environments[0].Name != cohostedName {
		t.Fatalf("environment name changed after peer detach: solo=%q cohosted=%q", soloDS.Environments[0].Name, cohostedName)
	}
}
