package envoy

import (
	"testing"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

func TestSnapshotClusterNamesIgnoresStaleEndpointClusters(t *testing.T) {
	names := snapshotClusterNames(snapshotParams{
		clusterName: "default",
		endpoints: map[string]*endpointState{
			"default": {},
			"stale":   {},
		},
	})

	if len(names) != 1 || names[0] != "default" {
		t.Fatalf("unexpected clusters: %+v", names)
	}
}

func TestBuildVirtualHostsMapsBlankHostnameRoutesToWildcardDomains(t *testing.T) {
	virtualHosts := buildVirtualHosts(nil, "default", []*desiredstatepb.IngressRoute{{
		Match:  &desiredstatepb.IngressMatch{Hostname: "", PathPrefix: "/"},
		Target: &desiredstatepb.IngressTarget{Environment: "prod", Service: "web", Port: "http"},
	}}, false, nil)

	if len(virtualHosts) != 1 {
		t.Fatalf("virtual hosts = %+v", virtualHosts)
	}
	if got := virtualHosts[0].GetDomains(); len(got) != 1 || got[0] != "*" {
		t.Fatalf("domains = %#v, want wildcard", got)
	}
	if len(virtualHosts[0].GetRoutes()) != 1 {
		t.Fatalf("routes = %#v", virtualHosts[0].GetRoutes())
	}
	if cluster := virtualHosts[0].GetRoutes()[0].GetRoute().GetCluster(); cluster != "env-prod-web-http" {
		t.Fatalf("cluster = %q", cluster)
	}
}
