package workflow

import (
	"bytes"
	"strings"
	"testing"

	"github.com/devopsellence/cli/internal/output"
)

func TestNewSoloInstallReporterJSONDiscardsInstallNoise(t *testing.T) {
	var out bytes.Buffer
	reporter := newSoloInstallReporter(t.Context(), output.Printer{Out: &out, JSON: true}, "prod-2")

	reporter.Progress("Installing Docker, agent, and systemd service...")
	if _, err := reporter.Stream().Write([]byte("progress: downloading agent binary\nplain log\npartial")); err != nil {
		t.Fatal(err)
	}
	reporter.Close()

	if got := out.String(); got != "" {
		t.Fatalf("reporter output = %q, want no unstructured output in JSON mode", got)
	}
}

func TestNewSoloInstallReporterPlainLinesWhenJSONDisabled(t *testing.T) {
	var out bytes.Buffer
	reporter := newSoloInstallReporter(t.Context(), output.Printer{Out: &out}, "prod-2")

	reporter.Progress("Installing Docker, agent, and systemd service...")
	if _, err := reporter.Stream().Write([]byte("progress: downloading agent binary\nplain log\npartial")); err != nil {
		t.Fatal(err)
	}
	reporter.Close()

	text := out.String()
	for _, fragment := range []string{
		"[prod-2] Installing Docker, agent, and systemd service...",
		"[prod-2] downloading agent binary",
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
