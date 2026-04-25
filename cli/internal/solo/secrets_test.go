package solo

import "testing"

func TestStateSecretsCRUDScopesByWorkspaceEnvironmentAndService(t *testing.T) {
	root := t.TempDir()
	otherRoot := t.TempDir()
	current := newState()

	if _, err := current.SetSecret(root, "production", "web", "DATABASE_URL", SecretMaterial{Value: "postgres://prod-web"}); err != nil {
		t.Fatal(err)
	}
	if _, err := current.SetSecret(root, "production", "worker", "DATABASE_URL", SecretMaterial{Value: "postgres://prod-worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := current.SetSecret(root, "staging", "web", "DATABASE_URL", SecretMaterial{Value: "postgres://staging-web"}); err != nil {
		t.Fatal(err)
	}
	if _, err := current.SetSecret(otherRoot, "production", "web", "DATABASE_URL", SecretMaterial{Value: "postgres://other-web"}); err != nil {
		t.Fatal(err)
	}

	values, err := current.ScopedSecretValues(root, "production")
	if err != nil {
		t.Fatal(err)
	}
	if got := values.Value("web", "DATABASE_URL"); got != "postgres://prod-web" {
		t.Fatalf("web DATABASE_URL = %q", got)
	}
	if got := values.Value("worker", "DATABASE_URL"); got != "postgres://prod-worker" {
		t.Fatalf("worker DATABASE_URL = %q", got)
	}

	secrets, err := current.ListSecrets(root, "production", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 2 {
		t.Fatalf("secrets = %#v, want 2 production records", secrets)
	}
	if secrets[0].ServiceName != "web" || secrets[0].Name != "DATABASE_URL" {
		t.Fatalf("first secret = %#v", secrets[0])
	}
	if secrets[0].Value != "" {
		t.Fatalf("listed secret exposed value: %#v", secrets[0])
	}

	if _, err := current.DeleteSecret(root, "production", "web", "DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	values, err = current.ScopedSecretValues(root, "production")
	if err != nil {
		t.Fatal(err)
	}
	if got := values.Value("web", "DATABASE_URL"); got != "" {
		t.Fatalf("deleted web DATABASE_URL = %q", got)
	}
	if got := values.Value("worker", "DATABASE_URL"); got != "postgres://prod-worker" {
		t.Fatalf("worker DATABASE_URL = %q", got)
	}
}

func TestStateSecretValidation(t *testing.T) {
	current := newState()
	if _, err := current.SetSecret(t.TempDir(), "production", "", "DATABASE_URL", SecretMaterial{Value: "value"}); err == nil {
		t.Fatal("SetSecret missing service error = nil")
	}
	if _, err := current.SetSecret(t.TempDir(), "production", "web", "", SecretMaterial{Value: "value"}); err == nil {
		t.Fatal("SetSecret missing name error = nil")
	}
}

func TestStateSecretOnePasswordReference(t *testing.T) {
	current := newState()
	record, err := current.SetSecret(t.TempDir(), "production", "web", "DATABASE_URL", SecretMaterial{
		Store:     SecretStoreOnePassword,
		Reference: "op://app-prod/db/password",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Store != SecretStoreOnePassword || record.Reference != "op://app-prod/db/password" || record.Value != "" {
		t.Fatalf("record = %#v", record)
	}

	if _, err := current.SetSecret(t.TempDir(), "production", "web", "BROKEN", SecretMaterial{
		Store:     SecretStoreOnePassword,
		Reference: "not-op-ref",
	}); err == nil {
		t.Fatal("SetSecret invalid 1Password reference error = nil")
	}
}
