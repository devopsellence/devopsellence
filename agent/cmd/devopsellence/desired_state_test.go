package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devopsellence/devopsellence/agent/internal/auth"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatecache"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

func TestRunDesiredStatePathsUsesAuthStateDefaults(t *testing.T) {
	authStatePath := filepath.Join(t.TempDir(), "agent-auth-state.json")
	var stdout bytes.Buffer

	if err := runDesiredState([]string{"paths", "--auth-state-path", authStatePath}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("paths: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "cache_path="+filepath.Join(filepath.Dir(authStatePath), "desired-state-cache.json")) {
		t.Fatalf("unexpected cache path output: %s", output)
	}
	if !strings.Contains(output, "override_path="+filepath.Join(filepath.Dir(authStatePath), "desired-state-override.json")) {
		t.Fatalf("unexpected override path output: %s", output)
	}
}

func TestRunDesiredStateSetOverrideAndShowActive(t *testing.T) {
	dir := t.TempDir()
	authStatePath := filepath.Join(dir, "agent-auth-state.json")
	overrideSourcePath := filepath.Join(dir, "manual.json")
	if err := desiredstatecache.WriteOverride(overrideSourcePath, []byte(`{"enabled":true,"desired_state":{"schemaVersion":2,"revision":"manual-rev","environments":[{"name":"production","services":[{"name":"web","kind":"web","image":"nginx:latest","ports":[{"name":"http","port":80}],"healthcheck":{"path":"/up","port":80}}]}]}}`)); err != nil {
		t.Fatalf("write source override: %v", err)
	}

	var stdout bytes.Buffer
	if err := runDesiredState([]string{
		"set-override",
		"--auth-state-path", authStatePath,
		"--file", overrideSourcePath,
	}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("set-override: %v", err)
	}

	stdout.Reset()
	if err := runDesiredState([]string{"show-active", "--auth-state-path", authStatePath}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("show-active: %v", err)
	}
	if !strings.Contains(stdout.String(), `"manual-rev"`) {
		t.Fatalf("unexpected active desired state output: %s", stdout.String())
	}
}

func TestRunDesiredStateShowActiveFallsBackToCache(t *testing.T) {
	dir := t.TempDir()
	authStatePath := filepath.Join(dir, "agent-auth-state.json")
	cachePath := filepath.Join(dir, "desired-state-cache.json")
	store := desiredstatecache.New(cachePath)
	snapshot := auth.DesiredStateSnapshot{
		NodeID:        11,
		EnvironmentID: 44,
		Target: auth.DesiredStateTarget{
			Mode: "assigned",
			URI:  "gs://bucket/node-a.json",
		},
	}
	desired := &desiredstatepb.DesiredState{
		SchemaVersion: 2,
		Revision:      "cached-rev",
		Environments: []*desiredstatepb.Environment{{
			Name: "production",
			Services: []*desiredstatepb.Service{{
				Name:        "web",
				Kind:        "web",
				Image:       "nginx:latest",
				Ports:       []*desiredstatepb.ServicePort{{Name: "http", Port: 80}},
				Healthcheck: &desiredstatepb.Healthcheck{Path: "/up", Port: 80},
			}},
		}},
	}
	if err := store.Save(snapshot, 7, desired); err != nil {
		t.Fatalf("save cache: %v", err)
	}

	var stdout bytes.Buffer
	if err := runDesiredState([]string{"show-active", "--auth-state-path", authStatePath}, &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("show-active from cache: %v", err)
	}
	if !strings.Contains(stdout.String(), `"cached-rev"`) {
		t.Fatalf("unexpected cached desired state output: %s", stdout.String())
	}
}
