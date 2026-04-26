package workflow

import (
	"bytes"
	"testing"

	"github.com/devopsellence/cli/internal/output"
)

func TestNewSoloInstallReporterDiscardsInstallNoise(t *testing.T) {
	var out bytes.Buffer
	reporter := newSoloInstallReporter(t.Context(), output.Printer{Out: &out}, "prod-2")

	reporter.Progress("Installing Docker, agent, and systemd service...")
	if _, err := reporter.Stream().Write([]byte("progress: downloading agent binary\nplain log\npartial")); err != nil {
		t.Fatal(err)
	}
	reporter.Close()

	if got := out.String(); got != "" {
		t.Fatalf("reporter output = %q, want no unstructured output", got)
	}
}
