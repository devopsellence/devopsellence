package direct

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"

	"github.com/devopsellence/cli/internal/config"
)

func sshArgs(node config.DirectNode, command string) []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=accept-new",
		"-p", strconv.Itoa(node.Port),
	}
	if node.SSHKey != "" {
		args = append(args, "-i", node.SSHKey)
	}
	args = append(args, fmt.Sprintf("%s@%s", node.User, node.Host), command)
	return args
}

// RunSSH executes a command on a remote node via ssh.
// It inherits the user's ~/.ssh/config, agent forwarding, and known hosts.
// If stdin is non-nil it is piped to the remote command.
func RunSSH(ctx context.Context, node config.DirectNode, command string, stdin io.Reader) (string, error) {
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
func RunSSHInteractive(ctx context.Context, node config.DirectNode, command string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(node, command)...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh %s@%s: %w", node.User, node.Host, err)
	}
	return nil
}

func RunSSHInteractiveWithStdin(ctx context.Context, node config.DirectNode, command string, stdin io.Reader, stdout, stderr io.Writer) error {
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
func RunSSHStream(ctx context.Context, node config.DirectNode, command string, stdin io.Reader) error {
	cmd := exec.CommandContext(ctx, "ssh", sshArgs(node, command)...)
	cmd.Stdin = stdin

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ssh %s@%s: %w: %s", node.User, node.Host, err, stderr.String())
	}
	return nil
}
