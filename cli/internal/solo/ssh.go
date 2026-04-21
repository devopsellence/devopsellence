package solo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/devopsellence/cli/internal/config"
	"github.com/devopsellence/cli/internal/state"
)

func sshArgs(node config.SoloNode, command string) []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "StrictHostKeyChecking=accept-new",
		"-p", strconv.Itoa(node.Port),
	}
	if knownHostsPath := managedKnownHostsPath(node); knownHostsPath != "" {
		args = append(args, "-o", "UserKnownHostsFile="+knownHostsPath)
	}
	if node.SSHKey != "" {
		args = append(args, "-i", node.SSHKey)
	}
	args = append(args, fmt.Sprintf("%s@%s", node.User, node.Host), command)
	return args
}

func managedKnownHostsPath(node config.SoloNode) string {
	if node.Provider == "" || node.ProviderServerID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(node.Provider + "\x00" + node.ProviderServerID))
	filename := "managed-" + hex.EncodeToString(sum[:])[:16]
	return filepath.Join(state.DefaultPath(filepath.Join("devopsellence", "ssh_known_hosts")), filename)
}

func prepareSSH(node config.SoloNode) error {
	knownHostsPath := managedKnownHostsPath(node)
	if knownHostsPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(knownHostsPath), 0o755); err != nil {
		return fmt.Errorf("prepare ssh known_hosts for %s@%s: %w", node.User, node.Host, err)
	}
	return nil
}

// RunSSH executes a command on a remote node via ssh.
// It inherits the user's SSH config and agent behavior; for provider-managed
// nodes it uses a devopsellence-managed per-server known_hosts file under state.
// If stdin is non-nil it is piped to the remote command.
func RunSSH(ctx context.Context, node config.SoloNode, command string, stdin io.Reader) (string, error) {
	if err := prepareSSH(node); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(node, command)...)
	if stdin != nil {
		cmd.Stdin = stdin
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ssh %s@%s: %w: %s", node.User, node.Host, err, stderr.String())
	}
	return stdout.String(), nil
}

// RunSSHInteractive runs a command on a remote node, connecting stdout and
// stderr directly to the provided writers. Use this for long-running streaming
// commands like `journalctl -f` where output must not be buffered.
func RunSSHInteractive(ctx context.Context, node config.SoloNode, command string, stdout, stderr io.Writer) error {
	if err := prepareSSH(node); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(node, command)...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh %s@%s: %w", node.User, node.Host, err)
	}
	return nil
}

func RunSSHInteractiveWithStdin(ctx context.Context, node config.SoloNode, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := prepareSSH(node); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(node, command)...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh %s@%s: %w", node.User, node.Host, err)
	}
	return nil
}

// RunSSHStream executes a command on a remote node via ssh, streaming stdin
// from the provided reader. Unlike RunSSH it does not capture stdout.
// This is used for piping docker save output to docker load on the remote.
func RunSSHStream(ctx context.Context, node config.SoloNode, command string, stdin io.Reader) error {
	if err := prepareSSH(node); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(node, command)...)
	cmd.Stdin = stdin

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh %s@%s: %w: %s", node.User, node.Host, err, stderr.String())
	}
	return nil
}
