package ui

import "strings"

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
			lines = append(lines, r.Title(title)+"  "+r.Accent(status))
		} else {
			lines = append(lines, r.Title(title))
		}
	}

	for _, row := range card.Rows {
		label := strings.TrimSpace(row.Label)
		value := strings.TrimSpace(row.Value)
		if label == "" && value == "" {
			continue
		}
		lines = append(lines, r.renderRow(label, value))
	}

	if footer := strings.TrimSpace(card.Footer); footer != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, r.Muted(footer))
	}

	return strings.Join(lines, "\n")
}

func (r Renderer) renderRow(label, value string) string {
	if label == "" {
		return r.Value(value)
	}
	if value == "" {
		return r.Label(label)
	}
	return r.Label(label) + ": " + r.Value(value)
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
