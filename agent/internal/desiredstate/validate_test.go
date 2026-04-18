package desiredstate

import (
	"testing"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

func TestValidateNil(t *testing.T) {
	if err := Validate(nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateMissingRevision(t *testing.T) {
	state := desiredState(workerService("worker"))
	state.Revision = ""
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateMissingEnvironmentName(t *testing.T) {
	state := desiredState(workerService("worker"))
	state.Environments[0].Name = ""
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateMissingServiceName(t *testing.T) {
	state := desiredState(workerService(""))
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateMissingImage(t *testing.T) {
	state := desiredState(&desiredstatepb.Service{Name: "worker", Kind: "worker"})
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRejectsUnknownServiceKind(t *testing.T) {
	state := desiredState(&desiredstatepb.Service{Name: "worker", Kind: "cron", Image: "busybox"})
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateDuplicateServiceName(t *testing.T) {
	state := desiredState(workerService("worker"), workerService("worker"))
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRejectsSanitizedEnvironmentCollision(t *testing.T) {
	state := desiredState(workerService("worker"))
	state.Environments = []*desiredstatepb.Environment{
		{Name: "Prod API", Revision: "rev-1", Services: []*desiredstatepb.Service{workerService("worker")}},
		{Name: "prod-api", Revision: "rev-1", Services: []*desiredstatepb.Service{workerService("jobs")}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRejectsSanitizedServiceCollision(t *testing.T) {
	state := desiredState(workerService("web api"), workerService("web-api"))
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRejectsEmptySanitizedServiceName(t *testing.T) {
	state := desiredState(workerService("!!!"))
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateEmptyEnvKey(t *testing.T) {
	service := workerService("worker")
	service.Env = map[string]string{"": "x"}
	state := desiredState(service)
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateEmptySecretRefKey(t *testing.T) {
	service := workerService("worker")
	service.SecretRefs = map[string]string{"": "file://app/key"}
	state := desiredState(service)
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateEmptySecretRefValue(t *testing.T) {
	service := workerService("worker")
	service.SecretRefs = map[string]string{"API_KEY": ""}
	state := desiredState(service)
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateEnvSecretRefConflict(t *testing.T) {
	service := workerService("worker")
	service.Env = map[string]string{"API_KEY": "x"}
	service.SecretRefs = map[string]string{"API_KEY": "file://app/API_KEY"}
	state := desiredState(service)
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateOK(t *testing.T) {
	state := desiredState(workerService("default"), workerService("mailers"))
	if err := Validate(state); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWebRequiresHealthcheck(t *testing.T) {
	state := desiredState(&desiredstatepb.Service{
		Name:  "web",
		Kind:  "web",
		Image: "busybox",
		Ports: []*desiredstatepb.ServicePort{{Name: "http", Port: 3000}},
	})
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateIngressRequiresWeb(t *testing.T) {
	state := desiredState(workerService("worker"))
	state.Ingress = &desiredstatepb.Ingress{
		Hosts:       []string{"abc123.devopsellence.io"},
		TunnelToken: "tok",
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateIngressRequiresHosts(t *testing.T) {
	state := desiredState(webService())
	state.Ingress = &desiredstatepb.Ingress{TunnelToken: "tok"}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateIngressAllowsTokenSecretRef(t *testing.T) {
	state := desiredState(webService())
	state.Ingress = &desiredstatepb.Ingress{
		Hosts:                []string{"abc123.devopsellence.io"},
		TunnelTokenSecretRef: "gsm://projects/test/secrets/cloudflare/versions/latest",
	}
	if err := Validate(state); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateIngressAllowsPublicMode(t *testing.T) {
	state := desiredState(webService())
	state.Ingress = &desiredstatepb.Ingress{
		Mode:  "public",
		Hosts: []string{"abc123.devopsellence.io"},
	}
	if err := Validate(state); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateIngressRejectsUnsupportedMode(t *testing.T) {
	state := desiredState(webService())
	state.Ingress = &desiredstatepb.Ingress{
		Mode:  "bogus",
		Hosts: []string{"abc123.devopsellence.io"},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateIngressRouteTarget(t *testing.T) {
	state := desiredState(webService())
	state.Ingress = &desiredstatepb.Ingress{
		Mode:         "public",
		Hosts:        []string{"app.example.com"},
		Routes:       []*desiredstatepb.IngressRoute{route("app.example.com", "production", "web", "http")},
		RedirectHttp: true,
	}
	if err := Validate(state); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateIngressRoutesRequireHosts(t *testing.T) {
	state := desiredState(webService())
	state.Ingress = &desiredstatepb.Ingress{
		Mode:   "public",
		Routes: []*desiredstatepb.IngressRoute{route("app.example.com", "production", "web", "http")},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateIngressRouteHostnameMustExistInHosts(t *testing.T) {
	state := desiredState(webService())
	state.Ingress = &desiredstatepb.Ingress{
		Mode:   "public",
		Hosts:  []string{"other.example.com"},
		Routes: []*desiredstatepb.IngressRoute{route("app.example.com", "production", "web", "http")},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRejectsSanitizedPortCollision(t *testing.T) {
	service := webService()
	service.Ports = []*desiredstatepb.ServicePort{
		{Name: "http api", Port: 3000},
		{Name: "http-api", Port: 3001},
	}
	state := desiredState(service)
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRejectsUnnamedPort(t *testing.T) {
	service := webService()
	service.Ports = []*desiredstatepb.ServicePort{{Port: 3000}}
	state := desiredState(service)
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func desiredState(services ...*desiredstatepb.Service) *desiredstatepb.DesiredState {
	return &desiredstatepb.DesiredState{
		SchemaVersion: 2,
		Revision:      "rev-1",
		Environments: []*desiredstatepb.Environment{{
			Name:     "production",
			Revision: "rev-1",
			Services: services,
		}},
	}
}

func workerService(name string) *desiredstatepb.Service {
	return &desiredstatepb.Service{Name: name, Kind: "worker", Image: "busybox"}
}

func webService() *desiredstatepb.Service {
	return &desiredstatepb.Service{
		Name:        "web",
		Kind:        "web",
		Image:       "busybox",
		Ports:       []*desiredstatepb.ServicePort{{Name: "http", Port: 3000}},
		Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 3000},
	}
}

func route(hostname, env, service, port string) *desiredstatepb.IngressRoute {
	return &desiredstatepb.IngressRoute{
		Match:  &desiredstatepb.IngressMatch{Hostname: hostname},
		Target: &desiredstatepb.IngressTarget{Environment: env, Service: service, Port: port},
	}
}
