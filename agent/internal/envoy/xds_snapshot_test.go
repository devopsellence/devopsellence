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

func TestBuildVirtualHostsSkipsBlankHostnameRoutes(t *testing.T) {
	virtualHosts := buildVirtualHosts(nil, "default", []*desiredstatepb.IngressRoute{{
		Match:  &desiredstatepb.IngressMatch{Hostname: "", PathPrefix: "/"},
		Target: &desiredstatepb.IngressTarget{Environment: "prod", Service: "web", Port: "http"},
	}}, false, nil)

	if len(virtualHosts) != 0 {
		t.Fatalf("unexpected virtual hosts: %+v", virtualHosts)
	}
}
