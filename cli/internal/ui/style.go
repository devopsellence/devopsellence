package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var defaultTheme = Theme{
	Title: lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFF8E8")),
	Accent: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF9F1C")).
		Bold(true),
	Info: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#5BC0EB")),
	Success: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#6EEB83")).
		Bold(true),
	Error: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF6B6B")).
		Bold(true),
	Muted: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#9CA3AF")),
	Label: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFBF69")).
		Bold(true),
	Value: lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F3F4F6")),
	Card: lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#264653")).
		Padding(1, 2),
}

type Theme struct {
	Title   lipgloss.Style
	Accent  lipgloss.Style
	Info    lipgloss.Style
	Success lipgloss.Style
	Error   lipgloss.Style
	Muted   lipgloss.Style
	Label   lipgloss.Style
	Value   lipgloss.Style
	Card    lipgloss.Style
}

type Renderer struct {
	theme      Theme
	configured bool
}

func NewRenderer(theme Theme) Renderer {
	return Renderer{theme: theme, configured: true}
}

func DefaultRenderer() Renderer {
	return Renderer{theme: defaultTheme, configured: true}
}

func (r Renderer) Title(text string) string {
	return r.theme.Title.Render(strings.TrimSpace(text))
}

func (r Renderer) Accent(text string) string {
	return r.theme.Accent.Render(strings.TrimSpace(text))
}

func (r Renderer) Info(text string) string {
	return r.theme.Info.Render("[i] " + strings.TrimSpace(text))
}

func (r Renderer) Success(text string) string {
	return r.theme.Success.Render("[ok] " + strings.TrimSpace(text))
}

func (r Renderer) Error(text string) string {
	return r.theme.Error.Render("[x] " + strings.TrimSpace(text))
}

func (r Renderer) Muted(text string) string {
	return r.theme.Muted.Render(strings.TrimSpace(text))
}

func (r Renderer) Label(text string) string {
	return r.theme.Label.Render(strings.TrimSpace(text))
}

func (r Renderer) Value(text string) string {
	return r.theme.Value.Render(strings.TrimSpace(text))
}

func DefaultTheme() Theme {
	return defaultTheme
}

func (r Renderer) normalized() Renderer {
	if r.configured {
		return r
	}
	return DefaultRenderer()
}
