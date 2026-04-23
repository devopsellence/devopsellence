package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/keygen"
	"github.com/devopsellence/cli/internal/solo"
	"github.com/devopsellence/cli/internal/state"
	"golang.org/x/crypto/ssh"
)

type generatedSSHKeyPair struct {
	PrivateKeyPath string
	PublicKeyPath  string
	PublicKey      string
	Fingerprint    string
	Generated      bool
}

func generatedWorkspaceSSHKeyPath(workspaceRoot string) (string, error) {
	workspaceKey, err := solo.CanonicalWorkspaceKey(workspaceRoot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(workspaceKey))
	dir := filepath.Join("devopsellence", "solo", "keys", hex.EncodeToString(sum[:])[:16])
	return state.DefaultPath(filepath.Join(dir, "id_ed25519")), nil
}

func ensureGeneratedWorkspaceSSHKey(workspaceRoot string) (generatedSSHKeyPair, error) {
	privateKeyPath, err := generatedWorkspaceSSHKeyPath(workspaceRoot)
	if err != nil {
		return generatedSSHKeyPair{}, err
	}
	publicKeyPath := privateKeyPath + ".pub"
	privateExists, err := fileExists(privateKeyPath)
	if err != nil {
		return generatedSSHKeyPair{}, fmt.Errorf("stat generated SSH private key: %w", err)
	}
	publicExists, err := fileExists(publicKeyPath)
	if err != nil {
		return generatedSSHKeyPair{}, fmt.Errorf("stat generated SSH public key: %w", err)
	}
	switch {
	case privateExists && !publicExists:
		return generatedSSHKeyPair{}, fmt.Errorf("generated SSH keypair is incomplete: missing public key %s", publicKeyPath)
	case !privateExists && publicExists:
		return generatedSSHKeyPair{}, fmt.Errorf("generated SSH keypair is incomplete: missing private key %s", privateKeyPath)
	}

	pair, err := keygen.New(privateKeyPath, keygen.WithKeyType(keygen.Ed25519), keygen.WithWrite())
	if err != nil {
		return generatedSSHKeyPair{}, fmt.Errorf("ensure generated SSH keypair: %w", err)
	}
	if err := os.Chmod(filepath.Dir(privateKeyPath), 0o700); err != nil {
		return generatedSSHKeyPair{}, fmt.Errorf("chmod generated SSH key directory: %w", err)
	}
	if err := os.Chmod(privateKeyPath, 0o600); err != nil {
		return generatedSSHKeyPair{}, fmt.Errorf("chmod generated SSH private key: %w", err)
	}
	if err := os.Chmod(publicKeyPath, 0o644); err != nil {
		return generatedSSHKeyPair{}, fmt.Errorf("chmod generated SSH public key: %w", err)
	}

	publicKeyBytes, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return generatedSSHKeyPair{}, fmt.Errorf("read generated SSH public key: %w", err)
	}
	publicKey, err := validatedAuthorizedKeyForPair(publicKeyPath, privateKeyPath, publicKeyBytes, pair.PublicKey())
	if err != nil {
		return generatedSSHKeyPair{}, err
	}
	return generatedSSHKeyPair{
		PrivateKeyPath: privateKeyPath,
		PublicKeyPath:  publicKeyPath,
		PublicKey:      publicKey,
		Fingerprint:    ssh.FingerprintSHA256(pair.PublicKey()),
		Generated:      !privateExists,
	}, nil
}

func validatedAuthorizedKeyForPair(publicKeyPath, privateKeyPath string, rawPublicKey []byte, expected ssh.PublicKey) (string, error) {
	parsedPublicKey, _, _, _, err := ssh.ParseAuthorizedKey(rawPublicKey)
	if err != nil {
		return "", fmt.Errorf("parse generated SSH public key %s: %w", publicKeyPath, err)
	}
	if parsedPublicKey == nil || expected == nil || !publicKeysMatch(parsedPublicKey, expected) {
		return "", fmt.Errorf("generated SSH keypair mismatch: public key %s does not match private key %s", publicKeyPath, privateKeyPath)
	}
	return strings.TrimSpace(string(rawPublicKey)), nil
}

func normalizeSoloSSHKeySource(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "generate", "generated":
		return "generate", nil
	case "existing":
		return "existing", nil
	default:
		return "", fmt.Errorf("unsupported SSH key source %q: use generate or existing", value)
	}
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func publicKeysMatch(a, b ssh.PublicKey) bool {
	if a == nil || b == nil {
		return false
	}
	return string(a.Marshal()) == string(b.Marshal())
}
