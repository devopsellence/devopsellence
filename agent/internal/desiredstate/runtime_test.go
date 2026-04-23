package desiredstate

import (
	"testing"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

func TestEffectiveIngressReturnsExplicitIngressUnchanged(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Ingress: &desiredstatepb.Ingress{
			Mode:  "public",
			Hosts: []string{"app.example.com"},
			Tls:   &desiredstatepb.IngressTLS{Mode: "off"},
			Routes: []*desiredstatepb.IngressRoute{{
				Match:  &desiredstatepb.IngressMatch{Hostname: "app.example.com", PathPrefix: "/"},
				Target: &desiredstatepb.IngressTarget{Environment: "production", Service: "web", Port: "http"},
			}},
		},
	}

	effective := EffectiveIngress(state)
	if effective == nil {
		t.Fatal("expected effective ingress")
	}
	if effective == state.Ingress {
		t.Fatal("expected cloned ingress, got original pointer")
	}
	if effective.GetMode() != "public" || len(effective.GetHosts()) != 1 || effective.GetHosts()[0] != "app.example.com" {
		t.Fatalf("unexpected ingress: %+v", effective)
	}
}

func TestEffectiveIngressSynthesizesSingleEnvironmentSingleWeb(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Environments: []*desiredstatepb.Environment{{
			Name: "production",
			Services: []*desiredstatepb.Service{{
				Name: "web",
				Kind: ServiceKindWeb,
				Ports: []*desiredstatepb.ServicePort{{
					Name: DefaultHTTPPortName,
					Port: 3000,
				}},
			}},
		}},
	}

	effective := EffectiveIngress(state)
	if effective == nil {
		t.Fatal("expected synthesized ingress")
	}
	if effective.GetMode() != "public" {
		t.Fatalf("mode = %q, want public", effective.GetMode())
	}
	if effective.GetTls().GetMode() != "off" {
		t.Fatalf("tls mode = %q, want off", effective.GetTls().GetMode())
	}
	if len(effective.GetHosts()) != 0 {
		t.Fatalf("hosts = %#v, want none for wildcard ip routing", effective.GetHosts())
	}
	if len(effective.GetRoutes()) != 1 {
		t.Fatalf("routes = %#v", effective.GetRoutes())
	}
	route := effective.GetRoutes()[0]
	if route.GetMatch().GetHostname() != "" {
		t.Fatalf("hostname = %q, want blank wildcard match", route.GetMatch().GetHostname())
	}
	if route.GetTarget().GetEnvironment() != "production" || route.GetTarget().GetService() != "web" || route.GetTarget().GetPort() != DefaultHTTPPortName {
		t.Fatalf("unexpected route target: %+v", route.GetTarget())
	}
}

func TestEffectiveIngressSkipsSynthesisForMultipleEnvironments(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Environments: []*desiredstatepb.Environment{
			{Name: "production", Services: []*desiredstatepb.Service{{Name: "web", Kind: ServiceKindWeb}}},
			{Name: "staging", Services: []*desiredstatepb.Service{{Name: "web", Kind: ServiceKindWeb}}},
		},
	}

	if effective := EffectiveIngress(state); effective != nil {
		t.Fatalf("expected no synthesized ingress, got %+v", effective)
	}
}

func TestEffectiveIngressSkipsSynthesisForMultipleWebServices(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Environments: []*desiredstatepb.Environment{{
			Name: "production",
			Services: []*desiredstatepb.Service{
				{Name: "web", Kind: ServiceKindWeb},
				{Name: "admin", Kind: ServiceKindWeb},
			},
		}},
	}

	if effective := EffectiveIngress(state); effective != nil {
		t.Fatalf("expected no synthesized ingress, got %+v", effective)
	}
}
