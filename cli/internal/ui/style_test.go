package ui

import (
	"strings"
	"testing"
)

func TestRendererHelpersKeepMessageText(t *testing.T) {
	renderer := DefaultRenderer()

	cases := []string{
		renderer.Info("auth ready"),
		renderer.Success("deploy ok"),
		renderer.Error("publish failed"),
		renderer.Muted("details"),
	}

	for _, text := range []string{"auth ready", "deploy ok", "publish failed", "details"} {
		found := false
		for _, rendered := range cases {
			if strings.Contains(rendered, text) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected rendered helpers to include %q", text)
		}
	}
}
