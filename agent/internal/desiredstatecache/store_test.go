package desiredstatecache

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/agent/internal/auth"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
)

func TestStoreSaveAndLoad(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "desired-state-cache.json"))
	store.now = func() time.Time { return time.Date(2026, 3, 27, 12, 0, 0, 0, time.UTC) }

	snapshot := auth.DesiredStateSnapshot{
		NodeID:        11,
		EnvironmentID: 44,
		SequenceFloor: 7,
		Target: auth.DesiredStateTarget{
			Mode:                    "assigned",
			URI:                     "gs://bucket/node-a.json",
			OrganizationBundleToken: "orgb-1",
			EnvironmentBundleToken:  "envb-1",
			NodeBundleToken:         "nodeb-1",
		},
	}
	desired := &desiredstatepb.DesiredState{Revision: "rev-1"}

	if err := store.Save(snapshot, 7, desired); err != nil {
		t.Fatalf("save: %v", err)
	}

	entry, loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if entry == nil || loaded == nil {
		t.Fatal("expected cached entry and desired state")
	}
	if entry.Sequence != 7 {
		t.Fatalf("unexpected sequence: %d", entry.Sequence)
	}
	if entry.URI != "gs://bucket/node-a.json" {
		t.Fatalf("unexpected uri: %s", entry.URI)
	}
	if entry.NodeBundleToken != "nodeb-1" {
		t.Fatalf("unexpected node bundle token: %s", entry.NodeBundleToken)
	}
	if loaded.Revision != "rev-1" {
		t.Fatalf("unexpected revision: %s", loaded.Revision)
	}
}
