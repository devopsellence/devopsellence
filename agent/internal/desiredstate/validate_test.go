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

func TestValidateMissingName(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision:   "rev-1",
		Containers: []*desiredstatepb.Container{{Image: "busybox:latest"}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateMissingImage(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision:   "rev-1",
		Containers: []*desiredstatepb.Container{{ServiceName: "worker"}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateDuplicateName(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision:   "rev-1",
		Containers: []*desiredstatepb.Container{{ServiceName: "worker", Image: "busybox"}, {ServiceName: "worker", Image: "busybox"}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateEmptyEnvKey(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision:   "rev-1",
		Containers: []*desiredstatepb.Container{{ServiceName: "worker", Image: "busybox", Env: map[string]string{"": "x"}}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateEmptySecretRefKey(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision:   "rev-1",
		Containers: []*desiredstatepb.Container{{ServiceName: "worker", Image: "busybox", SecretRefs: map[string]string{"": "file://app/key"}}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateEmptySecretRefValue(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision:   "rev-1",
		Containers: []*desiredstatepb.Container{{ServiceName: "worker", Image: "busybox", SecretRefs: map[string]string{"API_KEY": ""}}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateEnvSecretRefConflict(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Containers: []*desiredstatepb.Container{{
			ServiceName: "worker",
			Image:       "busybox",
			Env:         map[string]string{"API_KEY": "x"},
			SecretRefs:  map[string]string{"API_KEY": "file://app/API_KEY"},
		}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateOK(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision:   "rev-1",
		Containers: []*desiredstatepb.Container{{ServiceName: "worker", Image: "busybox", Env: map[string]string{"A": "B"}}},
	}
	if err := Validate(state); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMissingRevision(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Containers: []*desiredstatepb.Container{{ServiceName: "worker", Image: "busybox"}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateWebRequiresHealthcheck(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision:   "rev-1",
		Containers: []*desiredstatepb.Container{{ServiceName: "web", Image: "busybox", Port: 3000}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateIngressRequiresWeb(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Ingress: &desiredstatepb.Ingress{
			Hostname:    "abc123.devopsellence.io",
			TunnelToken: "tok",
		},
		Containers: []*desiredstatepb.Container{{ServiceName: "worker", Image: "busybox"}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateIngressRequiresHostname(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Ingress: &desiredstatepb.Ingress{
			TunnelToken: "tok",
		},
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "busybox",
			Port:        3000,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 3000},
		}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateIngressAllowsTokenSecretRef(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Ingress: &desiredstatepb.Ingress{
			Hostname:             "abc123.devopsellence.io",
			TunnelTokenSecretRef: "gsm://projects/test/secrets/cloudflare/versions/latest",
		},
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "busybox",
			Port:        3000,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 3000},
		}},
	}
	if err := Validate(state); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateIngressAllowsDirectDNSMode(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Ingress: &desiredstatepb.Ingress{
			Mode:     "direct_dns",
			Hostname: "abc123.devopsellence.io",
		},
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "busybox",
			Port:        3000,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 3000},
		}},
	}
	if err := Validate(state); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateIngressRejectsUnsupportedMode(t *testing.T) {
	state := &desiredstatepb.DesiredState{
		Revision: "rev-1",
		Ingress: &desiredstatepb.Ingress{
			Mode:     "bogus",
			Hostname: "abc123.devopsellence.io",
		},
		Containers: []*desiredstatepb.Container{{
			ServiceName: "web",
			Image:       "busybox",
			Port:        3000,
			Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 3000},
		}},
	}
	if err := Validate(state); err == nil {
		t.Fatal("expected error")
	}
}
