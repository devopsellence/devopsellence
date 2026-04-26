package ui

import "strings"

// Theme is retained as a compatibility shell while styling is removed.
type Theme struct{}

type Renderer struct {
	configured bool
}

func NewRenderer(_ Theme) Renderer {
	return Renderer{configured: true}
}

func DefaultRenderer() Renderer {
	return Renderer{configured: true}
}

func (r Renderer) Title(text string) string {
	return strings.TrimSpace(text)
}

func (r Renderer) Accent(text string) string {
	return strings.TrimSpace(text)
}

func (r Renderer) Info(text string) string {
	return "[i] " + strings.TrimSpace(text)
}

func (r Renderer) Success(text string) string {
	return "[ok] " + strings.TrimSpace(text)
}

func (r Renderer) Error(text string) string {
	return "[x] " + strings.TrimSpace(text)
}

func (r Renderer) Muted(text string) string {
	return strings.TrimSpace(text)
}

func (r Renderer) Label(text string) string {
	return strings.TrimSpace(text)
}

func (r Renderer) Value(text string) string {
	return strings.TrimSpace(text)
}

func DefaultTheme() Theme {
	return Theme{}
}

func (r Renderer) normalized() Renderer {
	if r.configured {
		return r
	}
	return DefaultRenderer()
}
