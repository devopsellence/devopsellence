package workflow

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devopsellence/cli/internal/output"
)

func TestInstallLogStateKeepsRecentLines(t *testing.T) {
	state := newInstallLogState(2)
	state.SetProgress("installing Docker Engine")
	state.AddLine("Get:1 packages")
	state.AddLine("Get:2 packages")
	state.AddLine("Get:3 packages")

	if got, want := state.StatusLine("[prod-2]"), "[+] [prod-2] installing Docker Engine"; got != want {
		t.Fatalf("StatusLine() = %q, want %q", got, want)
	}
	if got, want := state.ViewportContent(), "-> Get:2 packages\n-> Get:3 packages"; got != want {
		t.Fatalf("ViewportContent() = %q, want %q", got, want)
	}
}

func TestInstallLogModelPinsStatusAndShowsLatestLines(t *testing.T) {
	model := newInstallLogModel("[prod-2]", 2, 80)

	next, _ := model.Update(installProgressMsg{text: "downloading node agent binary"})
	model = next.(installLogModel)
	next, _ = model.Update(installLogLineMsg{text: "Get:1 packages"})
	model = next.(installLogModel)
	next, _ = model.Update(installLogLineMsg{text: "Get:2 packages"})
	model = next.(installLogModel)
	next, _ = model.Update(installLogLineMsg{text: "Get:3 packages"})
	model = next.(installLogModel)

	view := model.View()
	for _, fragment := range []string{
		"[+] [prod-2] downloading node agent binary",
		"-> Get:2 packages",
		"-> Get:3 packages",
	} {
		if !strings.Contains(view, fragment) {
			t.Fatalf("View() = %q, want fragment %q", view, fragment)
		}
	}
	if strings.Contains(view, "-> Get:1 packages") {
		t.Fatalf("View() = %q, want viewport scrolled past oldest line", view)
	}
}

func TestNewSoloInstallReporterNonInteractiveFallsBackToPrefixedLines(t *testing.T) {
	var out bytes.Buffer
	reporter := newSoloInstallReporter(t.Context(), output.Printer{Out: &out}, "prod-2")

	reporter.Progress("Installing Docker, node agent, and systemd service...")
	if _, err := reporter.Stream().Write([]byte("progress: downloading node agent binary\nplain log\npartial")); err != nil {
		t.Fatal(err)
	}
	reporter.Close()

	text := out.String()
	for _, fragment := range []string{
		"[prod-2] Installing Docker, node agent, and systemd service...",
		"[prod-2] downloading node agent binary",
		"[prod-2] plain log",
		"[prod-2] partial",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("reporter output = %q, want fragment %q", text, fragment)
		}
	}
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("reporter output = %q, want no ANSI redraw codes", text)
	}
}
