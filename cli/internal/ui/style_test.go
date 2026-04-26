package ui

import "testing"

func TestRendererReturnsPlainText(t *testing.T) {
	r := DefaultRenderer()
	if got := r.Success(" deployed "); got != "[ok] deployed" {
		t.Fatalf("Success() = %q", got)
	}
	if got := r.Error(" failed "); got != "[x] failed" {
		t.Fatalf("Error() = %q", got)
	}
}
