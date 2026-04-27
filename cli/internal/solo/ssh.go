package solo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/devopsellence/cli/internal/state"
	"github.com/devopsellence/devopsellence/deployment-core/pkg/deploycore/config"
)

func sshArgs(node config.Node, command string) []string {
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

func managedKnownHostsPath(node config.Node) string {
	identity := node.Provider + "\x00" + node.ProviderServerID
	prefix := "managed-"
	if node.Provider == "" || node.ProviderServerID == "" {
		identity = node.User + "\x00" + node.Host + "\x00" + strconv.Itoa(node.Port)
		prefix = "existing-"
	}
	sum := sha256.Sum256([]byte(identity))
	filename := prefix + hex.EncodeToString(sum[:])
	return filepath.Join(state.DefaultPath(filepath.Join("devopsellence", "ssh_known_hosts")), filename)
}

func RemoveKnownHosts(node config.Node) (bool, error) {
	path := managedKnownHostsPath(node)
	if path == "" {
		return false, nil
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("remove ssh known_hosts for %s@%s: %w", node.User, node.Host, err)
	}
	return true, nil
}

// SSHError preserves the underlying ssh process error and captured stderr.
// Callers that need to branch on remote command failures can inspect ExitCode
// without parsing the rendered error string.
type SSHError struct {
	User   string
	Host   string
	Err    error
	Stderr string
}

func (e *SSHError) Error() string {
	if e == nil {
		return "ssh error"
	}
	if e.Stderr != "" {
		return fmt.Sprintf("ssh %s@%s: %v: %s", e.User, e.Host, e.Err, e.Stderr)
	}
	return fmt.Sprintf("ssh %s@%s: %v", e.User, e.Host, e.Err)
}

func (e *SSHError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *SSHError) ExitCode() (int, bool) {
	if e == nil {
		return 0, false
	}
	var exitErr *exec.ExitError
	if !errors.As(e.Err, &exitErr) {
		return 0, false
	}
	return exitErr.ExitCode(), true
}

func prepareSSH(node config.Node) error {
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
// It inherits the user's SSH config and agent behavior, but stores host keys in
// a devopsellence-managed known_hosts file under state so node bootstrap does
// not depend on or mutate the operator's global ~/.ssh/known_hosts.
// If stdin is non-nil it is piped to the remote command.
func RunSSH(ctx context.Context, node config.Node, command string, stdin io.Reader) (string, error) {
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
		return "", &SSHError{User: node.User, Host: node.Host, Err: err, Stderr: stderr.String()}
	}
	return stdout.String(), nil
}

// RunSSHInteractive runs a command on a remote node, connecting stdout and
// stderr directly to the provided writers. Use this for long-running streaming
// commands like `journalctl -f` where output must not be buffered.
func RunSSHInteractive(ctx context.Context, node config.Node, command string, stdout, stderr io.Writer) error {
	if err := prepareSSH(node); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(node, command)...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return &SSHError{User: node.User, Host: node.Host, Err: err}
	}
	return nil
}

func RunSSHInteractiveWithStdin(ctx context.Context, node config.Node, command string, stdin io.Reader, stdout, stderr io.Writer) error {
	if err := prepareSSH(node); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(node, command)...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return &SSHError{User: node.User, Host: node.Host, Err: err}
	}
	return nil
}

// RunSSHStream executes a command on a remote node via ssh, streaming stdin
// from the provided reader. Unlike RunSSH it does not capture stdout.
// This is used for piping docker save output to docker load on the remote.
func RunSSHStream(ctx context.Context, node config.Node, command string, stdin io.Reader) error {
	if err := prepareSSH(node); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(node, command)...)
	cmd.Stdin = stdin

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return &SSHError{User: node.User, Host: node.Host, Err: err, Stderr: stderr.String()}
	}
	return nil
}
