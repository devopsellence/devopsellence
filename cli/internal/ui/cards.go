package ui

import (
	"fmt"
	"strings"
)

type Row struct {
	Label string
	Value string
}

type Card struct {
	Title  string
	Status string
	Rows   []Row
	Footer string
}

func RenderCard(card Card) string {
	return DefaultRenderer().Card(card)
}

func (r Renderer) Card(card Card) string {
	r = r.normalized()
	lines := make([]string, 0, len(card.Rows)+3)
	title := strings.TrimSpace(card.Title)
	status := strings.TrimSpace(card.Status)

	if title != "" {
		if status != "" {
			lines = append(lines, fmt.Sprintf("%s  %s", r.Title(title), r.Accent(status)))
		} else {
			lines = append(lines, r.Title(title))
		}
	}

	labelWidth := 0
	for _, row := range card.Rows {
		if width := len(strings.TrimSpace(row.Label)); width > labelWidth {
			labelWidth = width
		}
	}

	for _, row := range card.Rows {
		label := strings.TrimSpace(row.Label)
		value := strings.TrimSpace(row.Value)
		if label == "" && value == "" {
			continue
		}
		lines = append(lines, r.renderRow(labelWidth, label, value))
	}

	if footer := strings.TrimSpace(card.Footer); footer != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, r.Muted(footer))
	}

	return strings.Join(lines, "\n")
}

func (r Renderer) renderRow(labelWidth int, label, value string) string {
	return fmt.Sprintf("%-*s  %s", labelWidth, r.Label(label), r.Value(value))
}

func RenderCommandBlock(title, command string) string {
	return DefaultRenderer().CommandBlock(title, command)
}

func (r Renderer) CommandBlock(title, command string) string {
	r = r.normalized()
	lines := []string{}
	if t := strings.TrimSpace(title); t != "" {
		lines = append(lines, r.Muted(t), "")
	}
	lines = append(lines, r.Accent(strings.TrimSpace(command)))
	return strings.Join(lines, "\n")
}
