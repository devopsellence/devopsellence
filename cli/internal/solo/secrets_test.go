package solo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSecrets_NoFile(t *testing.T) {
	secrets, err := LoadSecrets(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(secrets) != 0 {
		t.Fatalf("expected empty, got %d", len(secrets))
	}
}

func TestLoadSecrets_ParsesEnvFile(t *testing.T) {
	dir := t.TempDir()
	content := `# database config
DB_URL=postgres://localhost/db
API_KEY="secret 123"
export RAILS_ENV=production
QUOTED='single'

# blank lines and comments are fine
`
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	secrets, err := LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if secrets["DB_URL"] != "postgres://localhost/db" {
		t.Errorf("DB_URL = %q", secrets["DB_URL"])
	}
	if secrets["API_KEY"] != "secret 123" {
		t.Errorf("API_KEY = %q (expected unquoted)", secrets["API_KEY"])
	}
	if secrets["RAILS_ENV"] != "production" {
		t.Errorf("RAILS_ENV = %q (expected export prefix stripped)", secrets["RAILS_ENV"])
	}
	if secrets["QUOTED"] != "single" {
		t.Errorf("QUOTED = %q (expected single quotes stripped)", secrets["QUOTED"])
	}
	if len(secrets) != 4 {
		t.Errorf("expected 4 keys, got %d", len(secrets))
	}
}

func TestSecretsCRUD(t *testing.T) {
	dir := t.TempDir()

	// Save two secrets (creates .env).
	if err := SaveSecret(dir, "DB_URL", "postgres://localhost/db"); err != nil {
		t.Fatal(err)
	}
	if err := SaveSecret(dir, "API_KEY", "secret123"); err != nil {
		t.Fatal(err)
	}

	// List.
	keys, err := ListSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys[0] != "API_KEY" || keys[1] != "DB_URL" {
		t.Errorf("expected [API_KEY DB_URL], got %v", keys)
	}

	// Update existing.
	if err := SaveSecret(dir, "DB_URL", "postgres://prod/db"); err != nil {
		t.Fatal(err)
	}
	secrets, err := LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if secrets["DB_URL"] != "postgres://prod/db" {
		t.Errorf("expected updated value, got %q", secrets["DB_URL"])
	}

	// Delete.
	if err := DeleteSecret(dir, "API_KEY"); err != nil {
		t.Fatal(err)
	}
	keys, err = ListSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "DB_URL" {
		t.Errorf("expected [DB_URL] after delete, got %v", keys)
	}

	// Delete nonexistent.
	if err := DeleteSecret(dir, "NOPE"); err == nil {
		t.Error("expected error deleting nonexistent secret")
	}

	// File permissions.
	info, err := os.Stat(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("expected 0600 permissions, got %o", info.Mode().Perm())
	}
}

func TestSaveSecret_PreservesComments(t *testing.T) {
	dir := t.TempDir()
	initial := "# My secrets\nDB_URL=old\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SaveSecret(dir, "DB_URL", "new"); err != nil {
		t.Fatal(err)
	}
	if err := SaveSecret(dir, "API_KEY", "abc"); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".env"))
	content := string(data)
	if content != "# My secrets\nDB_URL=new\nAPI_KEY=abc\n" {
		t.Errorf("unexpected content:\n%s", content)
	}
}

func TestQuoteIfNeeded(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"has space", `"has space"`},
		{"has#hash", `"has#hash"`},
		{`has"quote`, `"has\"quote"`},
		{`back\slash`, `"back\\slash"`},
	}
	for _, tt := range tests {
		got := quoteIfNeeded(tt.in)
		if got != tt.want {
			t.Errorf("quoteIfNeeded(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSaveLoadRoundtrip_SpecialChars(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"PLAIN":     "hello",
		"SPACES":    "hello world",
		"QUOTES":    `say "hi"`,
		"BACKSLASH": `path\to\thing`,
		"MIXED":     `a "b" c\d`,
		"HASH":      "before#after",
	}
	for k, v := range cases {
		if err := SaveSecret(dir, k, v); err != nil {
			t.Fatalf("save %s: %v", k, err)
		}
	}
	got, err := LoadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range cases {
		if got[k] != want {
			t.Errorf("%s: got %q, want %q", k, got[k], want)
		}
	}
}
