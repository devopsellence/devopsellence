package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	agentBin    = "/usr/local/bin/devopsellence-agent"
	envDir      = "/etc/devopsellence"
	envFile     = "/etc/devopsellence/agent.env"
	serviceFile = "/etc/systemd/system/devopsellence-agent.service"
	stateDir    = "/var/lib/devopsellence"
	authState   = "/var/lib/devopsellence/agent-auth-state.json"
	statusFile  = "/var/lib/devopsellence/status.json"
	networkName = "devopsellence"
)

func runUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	purgeRuntime := fs.Bool("purge-runtime", false, "also remove managed Docker containers and network")
	if err := fs.Parse(args); err != nil {
		return err
	}

	run := func(name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run() // best-effort; uninstall continues on error
	}

	hasSystemctl := commandExists("systemctl")

	if hasSystemctl {
		fmt.Println("Stopping and disabling devopsellence-agent service...")
		run("systemctl", "disable", "--now", "devopsellence-agent")
	}

	removeFile(serviceFile)

	if hasSystemctl {
		run("systemctl", "daemon-reload")
		run("systemctl", "reset-failed", "devopsellence-agent")
	}

	fmt.Println("Removing agent files...")
	removeFile(agentBin)
	removeFile(envFile)
	removeFile(authState)
	removeFile(statusFile)
	removeDir(envDir)
	removeDir(stateDir)

	if *purgeRuntime {
		if err := purgeDockerRuntime(run); err != nil {
			fmt.Fprintf(os.Stderr, "warning: runtime purge incomplete: %v\n", err)
		}
	}

	fmt.Println("devopsellence agent uninstalled.")
	if *purgeRuntime {
		fmt.Println("Managed Docker runtime resources removed.")
	} else {
		fmt.Println("Managed Docker runtime resources left intact; rerun with --purge-runtime to remove them.")
	}
	return nil
}

func purgeDockerRuntime(run func(string, ...string)) error {
	if !commandExists("docker") {
		fmt.Println("Docker CLI not found; skipping runtime purge.")
		return nil
	}

	fmt.Println("Removing managed Docker containers...")
	ids1, _ := dockerOutput("docker", "ps", "-aq", "--filter", "label=devopsellence.managed=true")
	ids2, _ := dockerOutput("docker", "ps", "-aq", "--filter", "label=devopsellence.system")
	ids := dedupe(append(ids1, ids2...))
	if len(ids) > 0 {
		run("docker", append([]string{"rm", "-f"}, ids...)...)
	}

	fmt.Println("Removing devopsellence Docker network...")
	run("docker", "network", "rm", networkName)
	return nil
}

func dockerOutput(name string, args ...string) ([]string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func dedupe(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := ids[:0]
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func removeFile(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "warning: remove %s: %v\n", path, err)
	}
}

func removeDir(path string) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		// non-empty dir is fine to leave
	}
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
