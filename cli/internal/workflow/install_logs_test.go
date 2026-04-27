package workflow

import (
	"bytes"
	"testing"

	"github.com/devopsellence/cli/internal/output"
)

func TestNewSoloInstallReporterCapturesInstallNoiseWithoutPrinting(t *testing.T) {
	var out bytes.Buffer
	reporter := newSoloInstallReporter(t.Context(), output.Printer{Out: &out}, "prod-2")

	reporter.Progress("Installing Docker, agent, and systemd service...")
	if _, err := reporter.Stdout().Write([]byte("progress: downloading agent binary\nplain log\npartial")); err != nil {
		t.Fatal(err)
	}
	if _, err := reporter.Stderr().Write([]byte("stderr: package install failed")); err != nil {
		t.Fatal(err)
	}
	reporter.Close()

	if got := out.String(); got != "" {
		t.Fatalf("reporter output = %q, want no unstructured output", got)
	}
	if got := reporter.CapturedStdout(); got != "progress: downloading agent binary\nplain log\npartial" {
		t.Fatalf("captured stdout = %q", got)
	}
	if got := reporter.CapturedStderr(); got != "stderr: package install failed" {
		t.Fatalf("captured stderr = %q", got)
	}
}
