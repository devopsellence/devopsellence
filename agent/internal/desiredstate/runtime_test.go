package desiredstate

import (
	"testing"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

func TestWithEffectiveIngressReturnsOriginalStateWhenExplicitIngressPresent(t *testing.T) {
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

	normalized := WithEffectiveIngress(state)
	if normalized != state {
		t.Fatal("expected explicit-ingress state to be returned unchanged")
	}
}

func TestWithEffectiveIngressSynthesizesSingleEnvironmentSingleWeb(t *testing.T) {
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

	normalized := WithEffectiveIngress(state)
	if normalized == state {
		t.Fatal("expected synthesized state clone")
	}
	if state.GetIngress() != nil {
		t.Fatal("expected original state to remain ingress-free")
	}
	effective := normalized.GetIngress()
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

func TestWithEffectiveIngressLeavesStateUnchangedForMultipleEnvironments(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Environments: []*desiredstatepb.Environment{
			{Name: "production", Services: []*desiredstatepb.Service{{Name: "web", Kind: ServiceKindWeb}}},
			{Name: "staging", Services: []*desiredstatepb.Service{{Name: "web", Kind: ServiceKindWeb}}},
		},
	}

	if normalized := WithEffectiveIngress(state); normalized != state {
		t.Fatal("expected multi-environment state to remain unchanged")
	}
}

func TestWithEffectiveIngressLeavesStateUnchangedForMultipleWebServices(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Environments: []*desiredstatepb.Environment{{
			Name: "production",
			Services: []*desiredstatepb.Service{
				{Name: "web", Kind: ServiceKindWeb},
				{Name: "admin", Kind: ServiceKindWeb},
			},
		}},
	}

	if normalized := WithEffectiveIngress(state); normalized != state {
		t.Fatal("expected multi-web state to remain unchanged")
	}
}
