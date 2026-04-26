package workflow

import (
	"context"
	"io"

	"github.com/devopsellence/cli/internal/output"
)

type soloInstallReporter struct {
	progress func(string)
	stream   io.Writer
	close    func()
}

func newSoloInstallReporter(_ context.Context, printer output.Printer, nodeName string) soloInstallReporter {
	return soloInstallReporter{
		progress: func(string) {},
		stream:   io.Discard,
		close:    func() {},
	}
}

func (r soloInstallReporter) Progress(message string) {
	if r.progress != nil {
		r.progress(message)
	}
}

func (r soloInstallReporter) Stream() io.Writer {
	if r.stream == nil {
		return io.Discard
	}
	return r.stream
}

func (r soloInstallReporter) Close() {
	if r.close != nil {
		r.close()
	}
}
