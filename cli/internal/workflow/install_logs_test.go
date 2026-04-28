package workflow

import (
	"bytes"
	"testing"

	"github.com/devopsellence/cli/internal/output"
)

func TestNewSoloInstallReporterCapturesInstallNoiseAndEmitsStructuredProgress(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	reporter := newSoloInstallReporter(t.Context(), output.Printer{Out: &out, Err: &errOut}, "prod-2")

	reporter.Progress("Installing Docker, agent, and systemd service...")
	if _, err := reporter.Stdout().Write([]byte("progress: downloading agent binary\nplain log\npartial")); err != nil {
		t.Fatal(err)
	}
	if _, err := reporter.Stderr().Write([]byte("stderr: package install failed")); err != nil {
		t.Fatal(err)
	}
	reporter.Close()

	if got := out.String(); !bytes.Contains([]byte(got), []byte(`"event":"progress"`)) || !bytes.Contains([]byte(got), []byte(`"node":"prod-2"`)) {
		t.Fatalf("progress stdout = %q, want structured progress event", got)
	}
	if got := errOut.String(); got != "" {
		t.Fatalf("progress stderr = %q, want no command-contract output", got)
	}
	if got := reporter.CapturedStdout(); got != "progress: downloading agent binary\nplain log\npartial" {
		t.Fatalf("captured stdout = %q", got)
	}
	if got := reporter.CapturedStderr(); got != "stderr: package install failed" {
		t.Fatalf("captured stderr = %q", got)
	}
}
