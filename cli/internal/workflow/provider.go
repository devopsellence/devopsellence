package workflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/devopsellence/cli/internal/solo/providers"
	"github.com/devopsellence/cli/internal/state"
)

const (
	providerHetzner      = "hetzner"
	defaultHetznerRegion = "ash"
	defaultHetznerSize   = "cpx11"
)

type ProviderLoginOptions struct {
	Provider   string
	Token      string
	TokenStdin bool
}

type ProviderStatusOptions struct {
	Provider string
}

type ProviderLogoutOptions struct {
	Provider string
}

func (a *App) ProviderLogin(ctx context.Context, opts ProviderLoginOptions) error {
	providerSlug, err := normalizeProvider(opts.Provider)
	if err != nil {
		return ExitError{Code: 2, Err: err}
	}
	token, err := a.providerLoginToken(opts)
	if err != nil {
		return err
	}
	provider, err := providers.ResolveWithToken(providerSlug, token)
	if err != nil {
		return err
	}

	if err := provider.Validate(ctx); err != nil {
		return err
	}
	if err := saveProviderToken(a.ProviderState, providerSlug, token); err != nil {
		return err
	}

	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"provider":       providerSlug,
		"configured":     true,
		"source":         "state",
	})

}

func (a *App) ProviderStatus(ctx context.Context, opts ProviderStatusOptions) error {
	providerSlug, err := normalizeProvider(opts.Provider)
	if err != nil {
		return ExitError{Code: 2, Err: err}
	}
	token, source, err := providerToken(a.ProviderState, providerSlug)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) == "" {

		return a.Printer.PrintJSON(map[string]any{
			"schema_version": outputSchemaVersion,
			"provider":       providerSlug,
			"configured":     false,
		})

	}
	provider, err := providers.ResolveWithToken(providerSlug, token)
	if err != nil {
		return err
	}
	if err := provider.Validate(ctx); err != nil {
		return err
	}

	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"provider":       providerSlug,
		"configured":     true,
		"source":         source,
	})

}

func (a *App) ProviderLogout(_ context.Context, opts ProviderLogoutOptions) error {
	providerSlug, err := normalizeProvider(opts.Provider)
	if err != nil {
		return ExitError{Code: 2, Err: err}
	}
	deleted, err := deleteProviderToken(a.ProviderState, providerSlug)
	if err != nil {
		return err
	}

	return a.Printer.PrintJSON(map[string]any{
		"schema_version": outputSchemaVersion,
		"provider":       providerSlug,
		"deleted":        deleted,
	})

}

func (a *App) resolveSoloProvider(providerSlug string) (providers.Provider, error) {
	if a.soloProviderFn != nil {
		return a.soloProviderFn(providerSlug)
	}
	token, _, err := providerToken(a.ProviderState, providerSlug)
	if err != nil {
		return nil, err
	}
	return providers.ResolveWithToken(providerSlug, token)
}

func (a *App) ensureProviderTokenConfigured(ctx context.Context, provider string) error {
	providerSlug, err := normalizeProvider(provider)
	if err != nil {
		return err
	}
	token, _, err := providerToken(a.ProviderState, providerSlug)
	if err != nil {
		return err
	}
	if strings.TrimSpace(token) != "" {
		return nil
	}
	return ExitError{Code: 2, Err: fmt.Errorf("run `devopsellence provider login %s --token <token>` or configure DEVOPSELLENCE_HETZNER_API_TOKEN/HCLOUD_TOKEN", providerSlug)}
}

func (a *App) providerLoginToken(opts ProviderLoginOptions) (string, error) {
	if opts.TokenStdin {
		data, err := io.ReadAll(a.In)
		if err != nil {
			return "", ExitError{Code: 1, Err: err}
		}
		token := strings.TrimRight(string(data), "\r\n")
		if strings.TrimSpace(token) == "" {
			return "", ExitError{Code: 2, Err: errors.New("provider token is required")}
		}
		return token, nil
	}
	if strings.TrimSpace(opts.Token) != "" {
		return opts.Token, nil
	}
	return "", ExitError{Code: 2, Err: errors.New("missing required option: --token or --stdin")}
}

func normalizeProvider(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		provider = providerHetzner
	}
	switch provider {
	case providerHetzner:
		return provider, nil
	default:
		return "", fmt.Errorf("unsupported provider %q", provider)
	}
}

func providerToken(store *state.Store, provider string) (string, string, error) {
	if token, err := readProviderToken(store, provider); err != nil {
		return "", "", err
	} else if token != "" {
		return token, "state", nil
	}
	for _, name := range []string{"DEVOPSELLENCE_HETZNER_API_TOKEN", "HCLOUD_TOKEN"} {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value, name, nil
		}
	}
	return "", "", nil
}

func readProviderToken(store *state.Store, provider string) (string, error) {
	value, err := providerRecord(store, provider)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stringFromAny(value["token"])), nil
}

func saveProviderToken(store *state.Store, provider, token string) error {
	if store == nil {
		return errors.New("provider state store is required")
	}
	return store.Update(func(current map[string]any) (map[string]any, error) {
		providersMap := mapFromAny(current["providers"])
		providersMap[provider] = map[string]any{
			"token":      token,
			"updated_at": time.Now().UTC().Format(time.RFC3339),
		}
		current["providers"] = providersMap
		return current, nil
	})
}

func deleteProviderToken(store *state.Store, provider string) (bool, error) {
	if store == nil {
		return false, nil
	}
	deleted := false
	err := store.Update(func(current map[string]any) (map[string]any, error) {
		providersMap := mapFromAny(current["providers"])
		if _, ok := providersMap[provider]; ok {
			delete(providersMap, provider)
			deleted = true
		}
		current["providers"] = providersMap
		return current, nil
	})
	return deleted, err
}

func providerRecord(store *state.Store, provider string) (map[string]any, error) {
	if store == nil {
		return map[string]any{}, nil
	}
	current, err := store.Read()
	if err != nil {
		return nil, err
	}
	providersMap := mapFromAny(current["providers"])
	return mapFromAny(providersMap[provider]), nil
}

func mapFromAny(value any) map[string]any {
	result := map[string]any{}
	if value == nil {
		return result
	}
	source, ok := value.(map[string]any)
	if !ok {
		return result
	}
	for key, entry := range source {
		result[key] = entry
	}
	return result
}
