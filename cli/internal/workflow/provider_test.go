package workflow

import (
	"path/filepath"
	"testing"

	"github.com/devopsellence/cli/internal/state"
)

func TestProviderTokenStoreReadDelete(t *testing.T) {
	t.Parallel()

	store := state.New(filepath.Join(t.TempDir(), "providers.json"))
	if err := saveProviderToken(store, providerHetzner, "test-token"); err != nil {
		t.Fatal(err)
	}
	token, source, err := providerToken(store, providerHetzner)
	if err != nil {
		t.Fatal(err)
	}
	if token != "test-token" || source != "state" {
		t.Fatalf("providerToken = %q/%q, want test-token/state", token, source)
	}
	deleted, err := deleteProviderToken(store, providerHetzner)
	if err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("deleteProviderToken = false, want true")
	}
	token, source, err = providerToken(store, providerHetzner)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" || source != "" {
		t.Fatalf("providerToken after delete = %q/%q, want empty", token, source)
	}
}

func TestProviderTokenFallsBackToEnv(t *testing.T) {
	t.Setenv("DEVOPSELLENCE_HETZNER_API_TOKEN", "env-token")
	store := state.New(filepath.Join(t.TempDir(), "providers.json"))
	token, source, err := providerToken(store, providerHetzner)
	if err != nil {
		t.Fatal(err)
	}
	if token != "env-token" || source != "DEVOPSELLENCE_HETZNER_API_TOKEN" {
		t.Fatalf("providerToken = %q/%q, want env-token/DEVOPSELLENCE_HETZNER_API_TOKEN", token, source)
	}
}
