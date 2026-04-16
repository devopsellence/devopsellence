package providers

import "fmt"

func Resolve(slug string) (Provider, error) {
	return ResolveWithToken(slug, "")
}

func ResolveWithToken(slug, token string) (Provider, error) {
	switch slug {
	case "hetzner":
		return NewHetzner(token), nil
	default:
		return nil, fmt.Errorf("unsupported solo server provider %q", slug)
	}
}
