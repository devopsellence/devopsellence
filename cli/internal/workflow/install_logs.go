package workflow

import (
	"context"
	"io"
	"strings"

	"github.com/devopsellence/cli/internal/output"
)

type soloInstallReporter struct {
	progress func(string)
	stdout   io.Writer
	stderr   io.Writer
	close    func()
}

func newSoloInstallReporter(_ context.Context, printer output.Printer, node string) soloInstallReporter {
	return soloInstallReporter{
		progress: func(message string) {
			message = strings.TrimSpace(message)
			if message == "" {
				return
			}
			_ = printer.PrintEvent("progress", map[string]any{
				"operation": "devopsellence agent install",
				"node":      node,
				"message":   message,
			})
		},
		stdout: newTailBuffer(sshOutputTailLimit),
		stderr: newTailBuffer(sshOutputTailLimit),
		close:  func() {},
	}
}

func (r soloInstallReporter) Progress(message string) {
	if r.progress != nil {
		r.progress(message)
	}
}

func (r soloInstallReporter) Stdout() io.Writer {
	if r.stdout == nil {
		return io.Discard
	}
	return r.stdout
}

func (r soloInstallReporter) Stderr() io.Writer {
	if r.stderr == nil {
		return io.Discard
	}
	return r.stderr
}

func (r soloInstallReporter) Stream() io.Writer {
	return r.Stdout()
}

func (r soloInstallReporter) CapturedStdout() string {
	return capturedInstallOutput(r.stdout)
}

func (r soloInstallReporter) CapturedStderr() string {
	return capturedInstallOutput(r.stderr)
}

func capturedInstallOutput(writer io.Writer) string {
	if writer == nil {
		return ""
	}
	stringer, ok := writer.(interface{ String() string })
	if !ok {
		return ""
	}
	return stringer.String()
}

func (r soloInstallReporter) Close() {
	if r.close != nil {
		r.close()
	}
}
