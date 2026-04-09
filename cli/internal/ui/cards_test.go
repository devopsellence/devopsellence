package ui

import (
	"strings"
	"testing"
)

func TestRenderCardIncludesTitleRowsAndFooter(t *testing.T) {
	output := RenderCard(Card{
		Title:  "Deploy Complete",
		Status: "production",
		Rows: []Row{
			{Label: "Project", Value: "ShopApp"},
			{Label: "URL", Value: "https://shop.example.com"},
		},
		Footer: "release #42",
	})

	for _, expected := range []string{
		"Deploy Complete",
		"production",
		"Project",
		"ShopApp",
		"URL",
		"https://shop.example.com",
		"release #42",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected %q in output %q", expected, output)
		}
	}
}
