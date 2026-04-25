package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/devopsellence/cli/internal/config"
	"github.com/devopsellence/cli/internal/discovery"
	"github.com/devopsellence/cli/internal/solo"
	"github.com/devopsellence/cli/internal/ui"
)

type Mode string

const (
	ModeSolo   Mode = "solo"
	ModeShared Mode = "shared"
)

const modeUnsetError = "workspace mode is not set. Run `devopsellence mode use solo|shared`."

func normalizeMode(value string) (Mode, error) {
	switch strings.TrimSpace(strings.ToLower(value)) {
	case string(ModeSolo):
		return ModeSolo, nil
	case string(ModeShared):
		return ModeShared, nil
	default:
		return "", fmt.Errorf("unsupported mode %q: use solo or shared", value)
	}
}

func (a *App) modeWorkspaceKey() string {
	if discovered, err := discovery.Discover(a.Cwd); err == nil && strings.TrimSpace(discovered.WorkspaceRoot) != "" {
		if path, absErr := filepath.Abs(discovered.WorkspaceRoot); absErr == nil {
			return path
		}
		return discovered.WorkspaceRoot
	}
	if path, err := filepath.Abs(a.Cwd); err == nil {
		return path
	}
	return a.Cwd
}

func (a *App) savedMode() (Mode, bool, error) {
	if a.WorkspaceState == nil {
		return "", false, nil
	}
	current, err := a.WorkspaceState.Read()
	if err != nil {
		return "", false, err
	}
	modes, _ := current["modes"].(map[string]any)
	value := stringFromAny(modes[a.modeWorkspaceKey()])
	if strings.TrimSpace(value) == "" {
		return "", false, nil
	}
	mode, err := normalizeMode(value)
	if err != nil {
		return "", false, nil
	}
	return mode, true, nil
}

func (a *App) savedEnvironment() (string, bool, error) {
	if a.WorkspaceState == nil {
		return "", false, nil
	}
	current, err := a.WorkspaceState.Read()
	if err != nil {
		return "", false, err
	}
	environments, _ := current["environments"].(map[string]any)
	value := stringFromAny(environments[a.modeWorkspaceKey()])
	if strings.TrimSpace(value) == "" {
		return "", false, nil
	}
	return strings.TrimSpace(value), true, nil
}

func (a *App) SetMode(mode Mode) error {
	if a.WorkspaceState == nil {
		return nil
	}
	return a.WorkspaceState.Update(func(current map[string]any) (map[string]any, error) {
		modes, _ := current["modes"].(map[string]any)
		if modes == nil {
			modes = map[string]any{}
		}
		modes[a.modeWorkspaceKey()] = string(mode)
		current["modes"] = modes
		return current, nil
	})
}

func (a *App) SetEnvironment(name string) error {
	if a.WorkspaceState == nil {
		return nil
	}
	return a.WorkspaceState.Update(func(current map[string]any) (map[string]any, error) {
		environments, _ := current["environments"].(map[string]any)
		if environments == nil {
			environments = map[string]any{}
		}
		environments[a.modeWorkspaceKey()] = strings.TrimSpace(name)
		current["environments"] = environments
		return current, nil
	})
}

func (a *App) effectiveEnvironment(explicit string, cfg *config.Project) string {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit)
	}
	if value := strings.TrimSpace(os.Getenv("DEVOPSELLENCE_ENVIRONMENT")); value != "" {
		return value
	}
	if saved, ok, err := a.savedEnvironment(); err == nil && ok {
		return saved
	}
	if cfg != nil && strings.TrimSpace(cfg.DefaultEnvironment) != "" {
		return strings.TrimSpace(cfg.DefaultEnvironment)
	}
	return config.DefaultEnvironment
}

func (a *App) suggestedMode() Mode {
	discovered, err := discovery.Discover(a.Cwd)
	if err != nil {
		return ModeSolo
	}
	if a.workspaceHasSoloState(discovered.WorkspaceRoot) {
		return ModeSolo
	}
	return ModeShared
}

func (a *App) workspaceHasSoloState(workspaceRoot string) bool {
	if a.SoloState == nil {
		return false
	}
	current, err := a.SoloState.Read()
	if err != nil {
		return false
	}
	workspaceKey, err := solo.CanonicalWorkspaceKey(workspaceRoot)
	if err != nil {
		return false
	}
	for _, attachment := range current.Attachments {
		if attachment.WorkspaceKey == workspaceKey {
			return true
		}
	}
	for _, snapshot := range current.Snapshots {
		if snapshot.WorkspaceKey == workspaceKey {
			return true
		}
	}
	return false
}

func (a *App) ResolveMode(interactive bool) (Mode, error) {
	if saved, ok, err := a.savedMode(); err != nil {
		return "", ExitError{Code: 1, Err: err}
	} else if ok {
		return saved, nil
	}
	if !interactive {
		return "", ExitError{Code: 2, Err: errors.New(modeUnsetError)}
	}

	choice, err := a.promptLine("Workspace mode (solo/shared)", string(a.suggestedMode()))
	if err != nil {
		return "", ExitError{Code: 1, Err: err}
	}
	mode, err := normalizeMode(choice)
	if err != nil {
		return "", ExitError{Code: 2, Err: err}
	}
	if err := a.SetMode(mode); err != nil {
		return "", ExitError{Code: 1, Err: err}
	}
	return mode, nil
}

func (a *App) ResolveSetupMode(explicit string, interactive bool) (Mode, error) {
	if strings.TrimSpace(explicit) == "" {
		return a.ResolveMode(interactive)
	}
	mode, err := normalizeMode(explicit)
	if err != nil {
		return "", ExitError{Code: 2, Err: err}
	}
	if err := a.SetMode(mode); err != nil {
		return "", ExitError{Code: 1, Err: err}
	}
	return mode, nil
}

func (a *App) ModeShow() error {
	mode, ok, err := a.savedMode()
	if err != nil {
		return ExitError{Code: 1, Err: err}
	}
	if a.Printer.JSON {
		payload := map[string]any{
			"schema_version": outputSchemaVersion,
			"workspace_key":  a.modeWorkspaceKey(),
			"set":            ok,
		}
		if ok {
			payload["mode"] = string(mode)
		}
		return a.Printer.PrintJSON(payload)
	}
	if !ok {
		a.Printer.Println("Mode: not set")
		a.Printer.Println("Workspace:", a.modeWorkspaceKey())
		a.Printer.Println("Next step: run `devopsellence mode use solo|shared`.")
		return nil
	}
	a.Printer.Println("Mode:", mode)
	a.Printer.Println("Workspace:", a.modeWorkspaceKey())
	return nil
}

func (a *App) ContextShow() error {
	discovered, cfg, err := a.optionalWorkspaceConfig()
	if err != nil {
		// optionalWorkspaceConfig does not currently return sentinel errors, but
		// keep the call-site resilient if that changes.
		return ExitError{Code: 1, Err: err}
	}
	mode, ok, modeErr := a.savedMode()
	if modeErr != nil {
		return ExitError{Code: 1, Err: modeErr}
	}
	result := map[string]any{
		"schema_version": outputSchemaVersion,
		"workspace_root": discovered.WorkspaceRoot,
		"mode_set":       ok,
	}
	if ok {
		result["mode"] = string(mode)
	}
	if cfg != nil {
		result["organization"] = cfg.Organization
		result["project"] = cfg.Project
		result["default_environment"] = cfg.DefaultEnvironment
	}
	selectedEnvironment := a.effectiveEnvironment("", cfg)
	result["environment"] = selectedEnvironment
	result["selected_environment"] = selectedEnvironment
	if a.Printer.JSON {
		return a.Printer.PrintJSON(result)
	}
	rows := []ui.Row{
		{Label: "Workspace", Value: firstNonEmpty(discovered.WorkspaceRoot, a.modeWorkspaceKey())},
		{Label: "Mode", Value: firstNonEmpty(string(mode), "not set")},
		{Label: "Organization", Value: safeConfigValue(cfg, func(value *config.Project) string { return value.Organization })},
		{Label: "Project", Value: safeConfigValue(cfg, func(value *config.Project) string { return value.Project })},
		{Label: "Default Env", Value: safeConfigValue(cfg, func(value *config.Project) string { return value.DefaultEnvironment })},
		{Label: "Selected Env", Value: selectedEnvironment},
	}
	a.Printer.Println(ui.RenderCard(ui.Card{Title: "Context", Rows: rows}))
	if !ok {
		a.Printer.Println("Next step: run `devopsellence mode use solo|shared`.")
	}
	return nil
}
