package release

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/desiredstate"
)

func TestNewReleaseNormalizesImmutableSnapshot(t *testing.T) {
	env := map[string]string{"APP_ENV": "production"}
	release, err := NewRelease(ReleaseCreateInput{
		ID:            " rel-1 ",
		EnvironmentID: " env-1 ",
		Revision:      " abc1234 ",
		ConfigDigest:  " sha256:cfg ",
		Snapshot: desiredstate.DeploySnapshot{
			Revision:    "old",
			Image:       "shop:abc1234",
			Environment: "production",
			Services: []desiredstate.ServiceJSON{{
				Name: "web",
				Env:  env,
			}},
		},
		TargetNodeIDs: []string{"web-a", "web-a", " worker-a "},
		CreatedAt:     time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if release.ID != "rel-1" || release.EnvironmentID != "env-1" || release.Revision != "abc1234" {
		t.Fatalf("release identity = %#v", release)
	}
	if release.Snapshot.Revision != "abc1234" {
		t.Fatalf("snapshot revision = %q, want release revision", release.Snapshot.Revision)
	}
	if release.Image.Reference != "shop:abc1234" {
		t.Fatalf("image reference = %q", release.Image.Reference)
	}
	if got, want := strings.Join(release.TargetNodeIDs, ","), "web-a,worker-a"; got != want {
		t.Fatalf("target nodes = %q, want %q", got, want)
	}
	env["APP_ENV"] = "staging"
	if got := release.Snapshot.Services[0].Env["APP_ENV"]; got != "production" {
		t.Fatalf("release snapshot env = %q, want immutable production value", got)
	}
}

func TestSelectRollbackReleaseDefaultsToPreviousCreatedRelease(t *testing.T) {
	current := Release{ID: "rel-3", Revision: "ccc3333", CreatedAt: "2026-04-28T12:03:00Z"}
	previous := Release{ID: "rel-2", Revision: "bbb2222", CreatedAt: "2026-04-28T12:02:00Z"}
	oldest := Release{ID: "rel-1", Revision: "aaa1111", CreatedAt: "2026-04-28T12:01:00Z"}

	got, err := SelectRollbackRelease([]Release{oldest, current, previous}, current.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != previous.ID {
		t.Fatalf("rollback release = %s, want %s", got.ID, previous.ID)
	}
}

func TestSelectRollbackReleaseDefaultsToOlderThanCurrentWhenCurrentIsNotNewest(t *testing.T) {
	newest := Release{ID: "rel-4", Revision: "ddd4444", CreatedAt: "2026-04-28T12:04:00Z"}
	current := Release{ID: "rel-3", Revision: "ccc3333", CreatedAt: "2026-04-28T12:03:00Z"}
	previous := Release{ID: "rel-2", Revision: "bbb2222", CreatedAt: "2026-04-28T12:02:00Z"}
	oldest := Release{ID: "rel-1", Revision: "aaa1111", CreatedAt: "2026-04-28T12:01:00Z"}

	got, err := SelectRollbackRelease([]Release{oldest, previous, newest, current}, current.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != previous.ID {
		t.Fatalf("rollback release = %s, want %s", got.ID, previous.ID)
	}
}

func TestSelectRollbackReleaseDefaultsUsingParsedCreatedAt(t *testing.T) {
	current := Release{ID: "rel-3", Revision: "ccc3333", CreatedAt: "2026-04-28T08:03:00-04:00"}
	previous := Release{ID: "rel-2", Revision: "bbb2222", CreatedAt: "2026-04-28T12:02:00Z"}
	oldest := Release{ID: "rel-1", Revision: "aaa1111", CreatedAt: "2026-04-28T12:01:00Z"}

	got, err := SelectRollbackRelease([]Release{previous, oldest, current}, current.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != previous.ID {
		t.Fatalf("rollback release = %s, want %s", got.ID, previous.ID)
	}
}

func TestSelectRollbackReleaseDefaultsWithDeterministicTimestampTie(t *testing.T) {
	createdAt := "2026-04-28T12:00:00Z"
	current := Release{ID: "rel-b", Revision: "bbb2222", CreatedAt: createdAt}
	previous := Release{ID: "rel-a", Revision: "aaa1111", CreatedAt: createdAt}
	newerTie := Release{ID: "rel-c", Revision: "ccc3333", CreatedAt: createdAt}

	got, err := SelectRollbackRelease([]Release{previous, current, newerTie}, current.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != previous.ID {
		t.Fatalf("rollback release = %s, want %s", got.ID, previous.ID)
	}
}

func TestSelectRollbackReleaseRejectsInvalidCreatedAtForDefault(t *testing.T) {
	_, err := SelectRollbackRelease([]Release{
		{ID: "rel-1", Revision: "aaa1111", CreatedAt: "not-a-time"},
	}, "rel-1", "")
	if err == nil || !strings.Contains(err.Error(), "invalid created_at") {
		t.Fatalf("error = %v, want invalid created_at", err)
	}
}

func TestSelectRollbackReleaseRejectsAmbiguousRevisionPrefix(t *testing.T) {
	_, err := SelectRollbackRelease([]Release{
		{ID: "rel-1", Revision: "abc1111"},
		{ID: "rel-2", Revision: "abc2222"},
	}, "", "abc")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %v, want ambiguous", err)
	}
}

func TestPlanPublicationUsesReleaseSnapshots(t *testing.T) {
	plan, err := PlanPublication(PublicationPlanInput{
		NodeName: "web-a",
		Node: config.Node{
			Labels: []string{"web"},
		},
		Releases: []Release{
			{
				ID:       "rel-1",
				Revision: "abc1234",
				Snapshot: desiredstate.DeploySnapshot{
					WorkspaceKey: "/workspace/shop",
					Environment:  "production",
					Revision:     "abc1234",
					Services: []desiredstate.ServiceJSON{
						{Name: "web", Kind: "web", Image: "shop:abc1234"},
						{Name: "worker", Kind: "worker", Image: "shop:abc1234"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Revision     string `json:"revision"`
		Environments []struct {
			Services []struct {
				Name string `json:"name"`
			} `json:"services"`
		} `json:"environments"`
	}
	if err := json.Unmarshal(plan.DesiredStateJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Revision == "" || plan.Revision != payload.Revision {
		t.Fatalf("revision = plan:%q payload:%q", plan.Revision, payload.Revision)
	}
	if len(payload.Environments) != 1 || len(payload.Environments[0].Services) != 1 || payload.Environments[0].Services[0].Name != "web" {
		t.Fatalf("payload services = %#v", payload.Environments)
	}
}
