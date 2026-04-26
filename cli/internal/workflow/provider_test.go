package workflow

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devopsellence/cli/internal/output"
	"github.com/devopsellence/cli/internal/state"
)

func TestProviderTokenStoreReadDelete(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "")
	t.Setenv("DEVOPSELLENCE_HETZNER_API_TOKEN", "")

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

func TestEnsureInteractiveProviderLogin(t *testing.T) {
	t.Setenv("HCLOUD_TOKEN", "")
	t.Setenv("DEVOPSELLENCE_HETZNER_API_TOKEN", "")
	store := state.New(filepath.Join(t.TempDir(), "providers.json"))
	app := &App{
		Printer:       output.New(io.Discard, io.Discard, false),
		ProviderState: store,
	}
	app.Printer.Interactive = true

	original := runProviderLogin
	t.Cleanup(func() { runProviderLogin = original })

	var calls int
	runProviderLogin = func(gotApp *App, _ context.Context, opts ProviderLoginOptions) error {
		calls++
		if gotApp != app {
			t.Fatalf("runProviderLogin app = %#v, want %#v", gotApp, app)
		}
		if opts.Provider != providerHetzner {
			t.Fatalf("runProviderLogin provider = %q, want %q", opts.Provider, providerHetzner)
		}
		return nil
	}

	if err := app.ensureInteractiveProviderLogin(context.Background(), providerHetzner); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("runProviderLogin calls = %d, want 1", calls)
	}

	if err := saveProviderToken(store, providerHetzner, "stored-token"); err != nil {
		t.Fatal(err)
	}
	if err := app.ensureInteractiveProviderLogin(context.Background(), providerHetzner); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("runProviderLogin calls after stored token = %d, want 1", calls)
	}
}

func TestEnsureInteractiveProviderLoginSkipsEnvAndNonInteractive(t *testing.T) {
	t.Setenv("DEVOPSELLENCE_HETZNER_API_TOKEN", "env-token")

	original := runProviderLogin
	t.Cleanup(func() { runProviderLogin = original })

	runProviderLogin = func(*App, context.Context, ProviderLoginOptions) error {
		t.Fatal("runProviderLogin should not be called")
		return nil
	}

	app := &App{
		Printer:       output.New(io.Discard, io.Discard, false),
		ProviderState: state.New(filepath.Join(t.TempDir(), "providers.json")),
	}
	app.Printer.Interactive = true
	if err := app.ensureInteractiveProviderLogin(context.Background(), providerHetzner); err != nil {
		t.Fatal(err)
	}

	app.Printer.Interactive = false
	t.Setenv("DEVOPSELLENCE_HETZNER_API_TOKEN", "")
	t.Setenv("HCLOUD_TOKEN", "")
	err := app.ensureInteractiveProviderLogin(context.Background(), providerHetzner)
	if err == nil {
		t.Fatal("expected missing provider token error")
	}
	var exitErr ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 2 {
		t.Fatalf("error = %#v, want ExitError code 2", err)
	}
	if !strings.Contains(err.Error(), "devopsellence provider login hetzner --token <token>") {
		t.Fatalf("error = %v", err)
	}
}
