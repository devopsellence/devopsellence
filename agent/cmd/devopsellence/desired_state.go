package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/devopsellence/devopsellence/agent/internal/desiredstate"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatecache"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type desiredStatePaths struct {
	authStatePath string
	cachePath     string
	overridePath  string
}

func runDesiredState(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return desiredStateUsageError()
	}

	switch args[0] {
	case "paths":
		return runDesiredStatePaths(args[1:], stdout, stderr)
	case "show-cache":
		return runDesiredStateShowCache(args[1:], stdout, stderr)
	case "show-override":
		return runDesiredStateShowOverride(args[1:], stdout, stderr)
	case "show-active":
		return runDesiredStateShowActive(args[1:], stdout, stderr)
	case "validate-override":
		return runDesiredStateValidateOverride(args[1:], stdout, stderr)
	case "set-override":
		return runDesiredStateSetOverride(args[1:], stdout, stderr)
	case "clear-override":
		return runDesiredStateClearOverride(args[1:], stdout, stderr)
	default:
		return desiredStateUsageError()
	}
}

func desiredStateUsageError() error {
	return fmt.Errorf("usage: devopsellence desired-state <paths|show-cache|show-override|show-active|validate-override|set-override|clear-override>")
}

func runDesiredStatePaths(args []string, stdout, stderr io.Writer) error {
	paths, err := parseDesiredStatePathFlags("paths", args, stderr)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "auth_state_path=%s\ncache_path=%s\noverride_path=%s\n", paths.authStatePath, paths.cachePath, paths.overridePath)
	return nil
}

func runDesiredStateShowCache(args []string, stdout, stderr io.Writer) error {
	paths, err := parseDesiredStatePathFlags("show-cache", args, stderr)
	if err != nil {
		return err
	}
	return printFile(paths.cachePath, "desired state cache", stdout)
}

func runDesiredStateShowOverride(args []string, stdout, stderr io.Writer) error {
	paths, err := parseDesiredStatePathFlags("show-override", args, stderr)
	if err != nil {
		return err
	}
	return printFile(paths.overridePath, "desired state override", stdout)
}

func runDesiredStateShowActive(args []string, stdout, stderr io.Writer) error {
	paths, err := parseDesiredStatePathFlags("show-active", args, stderr)
	if err != nil {
		return err
	}

	if desired, active, err := desiredstatecache.LoadOverride(paths.overridePath); err != nil {
		return err
	} else if active {
		return printDesiredState(stdout, desired)
	}

	entry, desired, err := desiredstatecache.New(paths.cachePath).Load()
	if err != nil {
		return err
	}
	if entry == nil || desired == nil {
		return fmt.Errorf("no local desired state available")
	}
	return printDesiredState(stdout, desired)
}

func runDesiredStateValidateOverride(args []string, stdout, stderr io.Writer) error {
	paths, err := parseDesiredStatePathFlags("validate-override", args, stderr)
	if err != nil {
		return err
	}
	if _, err := os.Stat(paths.overridePath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("desired state override not found: %s", paths.overridePath)
		}
		return err
	}

	desired, active, err := desiredstatecache.LoadOverride(paths.overridePath)
	if err != nil {
		return err
	}
	if !active {
		_, _ = fmt.Fprintf(stdout, "override disabled: %s\n", paths.overridePath)
		return nil
	}
	if err := desiredstate.Validate(desired); err != nil {
		return fmt.Errorf("override invalid: %w", err)
	}
	_, _ = fmt.Fprintf(stdout, "override valid: %s\n", paths.overridePath)
	return nil
}

func runDesiredStateSetOverride(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("set-override", flag.ContinueOnError)
	fs.SetOutput(stderr)
	authStatePath := fs.String("auth-state-path", authState, "auth state path")
	cachePath := fs.String("cache-path", "", "desired state cache path")
	overridePath := fs.String("override-path", "", "desired state override path")
	filePath := fs.String("file", "", "override file path or - for stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*filePath) == "" {
		return fmt.Errorf("--file is required")
	}

	paths := resolveDesiredStatePaths(*authStatePath, *cachePath, *overridePath)
	data, err := readDesiredStateOverrideInput(*filePath)
	if err != nil {
		return err
	}
	if desired, active, err := desiredstatecache.ParseOverride(data); err != nil {
		return err
	} else if active {
		if err := desiredstate.Validate(desired); err != nil {
			return fmt.Errorf("override invalid: %w", err)
		}
	}
	if err := desiredstatecache.WriteOverride(paths.overridePath, data); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "override written: %s\n", paths.overridePath)
	return nil
}

func runDesiredStateClearOverride(args []string, stdout, stderr io.Writer) error {
	paths, err := parseDesiredStatePathFlags("clear-override", args, stderr)
	if err != nil {
		return err
	}
	if err := os.Remove(paths.overridePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove desired state override: %w", err)
	}
	_, _ = fmt.Fprintf(stdout, "override cleared: %s\n", paths.overridePath)
	return nil
}

func parseDesiredStatePathFlags(name string, args []string, stderr io.Writer) (desiredStatePaths, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	authStatePath := fs.String("auth-state-path", authState, "auth state path")
	cachePath := fs.String("cache-path", "", "desired state cache path")
	overridePath := fs.String("override-path", "", "desired state override path")
	if err := fs.Parse(args); err != nil {
		return desiredStatePaths{}, err
	}
	return resolveDesiredStatePaths(*authStatePath, *cachePath, *overridePath), nil
}

func resolveDesiredStatePaths(authStatePath, cachePath, overridePath string) desiredStatePaths {
	authStatePath = strings.TrimSpace(authStatePath)
	if authStatePath == "" {
		authStatePath = authState
	}
	if strings.TrimSpace(cachePath) == "" {
		cachePath = filepath.Join(filepath.Dir(authStatePath), "desired-state-cache.json")
	}
	if strings.TrimSpace(overridePath) == "" {
		overridePath = filepath.Join(filepath.Dir(authStatePath), "desired-state-override.json")
	}
	return desiredStatePaths{
		authStatePath: authStatePath,
		cachePath:     cachePath,
		overridePath:  overridePath,
	}
}

func printFile(path, label string, stdout io.Writer) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s not found: %s", label, path)
		}
		return fmt.Errorf("read %s: %w", label, err)
	}
	if len(data) == 0 {
		return nil
	}
	_, _ = stdout.Write(data)
	if data[len(data)-1] != '\n' {
		_, _ = stdout.Write([]byte("\n"))
	}
	return nil
}

func printDesiredState(stdout io.Writer, desired proto.Message) error {
	data, err := protojson.MarshalOptions{Multiline: true, Indent: "  "}.Marshal(desired)
	if err != nil {
		return fmt.Errorf("marshal desired state: %w", err)
	}
	_, _ = stdout.Write(data)
	_, _ = stdout.Write([]byte("\n"))
	return nil
}

func readDesiredStateOverrideInput(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read override from stdin: %w", err)
		}
		return bytes.TrimSpace(data), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read override file: %w", err)
	}
	return bytes.TrimSpace(data), nil
}
