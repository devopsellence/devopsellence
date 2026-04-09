package providers

import "fmt"

func Resolve(slug string) (Provider, error) {
	switch slug {
	case "hetzner":
		return NewHetznerFromEnv(), nil
	default:
		return nil, fmt.Errorf("unsupported direct server provider %q", slug)
	}
}
