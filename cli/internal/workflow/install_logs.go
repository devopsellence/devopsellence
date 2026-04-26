package workflow

import (
	"context"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/devopsellence/cli/internal/output"

	"golang.org/x/term"
)

const (
	defaultSoloInstallViewportLines = 8
	minSoloInstallViewportLines     = 4
	maxSoloInstallViewportLines     = 12
	defaultSoloInstallViewportWidth = 100
	maxSoloInstallBufferedLines     = 256
	defaultSoloInstallStatus        = "Installing Docker, node agent, and systemd service..."
)

type soloInstallReporter struct {
	progress func(string)
	stream   io.Writer
	close    func()
}

func newSoloInstallReporter(ctx context.Context, printer output.Printer, nodeName string) soloInstallReporter {
	if printer.JSON {
		return soloInstallReporter{
			progress: func(string) {},
			stream:   io.Discard,
			close:    func() {},
		}
	}

	if printer.Interactive {
		return newBubbleTeaSoloInstallReporter(ctx, printer.Out, nodeName)
	}

	progress := func(message string) {
		printer.Println("[" + nodeName + "] " + strings.TrimSpace(message))
	}
	writer := &lineProgressWriter{progress: progress}
	return soloInstallReporter{
		progress: progress,
		stream:   writer,
		close:    writer.Flush,
	}
}

func newBubbleTeaSoloInstallReporter(ctx context.Context, out io.Writer, nodeName string) soloInstallReporter {
	program := tea.NewProgram(
		newInstallLogModel("["+nodeName+"]", soloInstallViewportLines(out), soloInstallViewportWidth(out)),
		tea.WithContext(ctx),
		tea.WithInput(nil),
		tea.WithOutput(out),
	)
	writer := &bubbleTeaInstallLogWriter{program: program}
	done := make(chan struct{})
	go func() {
		_, _ = program.Run()
		close(done)
	}()
	return soloInstallReporter{
		progress: writer.Progress,
		stream:   writer,
		close: func() {
			writer.Close()
			<-done
		},
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

type installProgressMsg struct {
	text string
}

type installLogLineMsg struct {
	text string
}

type installDoneMsg struct{}

type installLogState struct {
	status   string
	lines    []string
	maxLines int
}

func newInstallLogState(maxLines int) installLogState {
	if maxLines <= 0 {
		maxLines = maxSoloInstallBufferedLines
	}
	return installLogState{maxLines: maxLines}
}

func (s *installLogState) SetProgress(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	s.status = message
}

func (s *installLogState) AddLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	s.lines = append(s.lines, line)
	if len(s.lines) > s.maxLines {
		s.lines = append([]string(nil), s.lines[len(s.lines)-s.maxLines:]...)
	}
}

func (s installLogState) StatusLine(nodeLabel string) string {
	status := strings.TrimSpace(s.status)
	if status == "" {
		status = defaultSoloInstallStatus
	}
	return "[+] " + strings.TrimSpace(nodeLabel) + " " + status
}

func (s installLogState) ViewportContent() string {
	if len(s.lines) == 0 {
		return ""
	}
	lines := make([]string, 0, len(s.lines))
	for _, line := range s.lines {
		lines = append(lines, "-> "+line)
	}
	return strings.Join(lines, "\n")
}

type installLogModel struct {
	nodeLabel string
	viewport  viewport.Model
	state     installLogState
}

func newInstallLogModel(nodeLabel string, height, width int) installLogModel {
	if height <= 0 {
		height = defaultSoloInstallViewportLines
	}
	if width <= 0 {
		width = defaultSoloInstallViewportWidth
	}
	vp := viewport.New(width, height)
	state := newInstallLogState(maxSoloInstallBufferedLines)
	model := installLogModel{
		nodeLabel: strings.TrimSpace(nodeLabel),
		viewport:  vp,
		state:     state,
	}
	model.syncViewport()
	return model
}

func (m installLogModel) Init() tea.Cmd {
	return nil
}

func (m installLogModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case installProgressMsg:
		m.state.SetProgress(typed.text)
		m.syncViewport()
	case installLogLineMsg:
		m.state.AddLine(typed.text)
		m.syncViewport()
	case installDoneMsg:
		return m, tea.Quit
	case tea.WindowSizeMsg:
		m.viewport.Width = typed.Width
		m.viewport.Height = soloInstallViewportLinesForHeight(typed.Height)
		m.syncViewport()
	}
	return m, nil
}

func (m installLogModel) View() string {
	header := m.state.StatusLine(m.nodeLabel)
	body := m.viewport.View()
	if body == "" {
		return header
	}
	return header + "\n" + body
}

func (m *installLogModel) syncViewport() {
	m.viewport.SetContent(m.state.ViewportContent())
	m.viewport.GotoBottom()
}

type bubbleTeaInstallLogWriter struct {
	mu      sync.Mutex
	buf     strings.Builder
	program *tea.Program
}

func (w *bubbleTeaInstallLogWriter) Progress(message string) {
	message = strings.TrimSpace(message)
	if message == "" || w.program == nil {
		return
	}
	w.program.Send(installProgressMsg{text: message})
}

func (w *bubbleTeaInstallLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range string(p) {
		switch r {
		case '\n':
			w.flushLocked()
		case '\r':
		default:
			w.buf.WriteRune(r)
		}
	}
	return len(p), nil
}

func (w *bubbleTeaInstallLogWriter) Close() {
	w.mu.Lock()
	w.flushLocked()
	w.mu.Unlock()
	if w.program != nil {
		w.program.Send(installDoneMsg{})
	}
}

func (w *bubbleTeaInstallLogWriter) flushLocked() {
	line := strings.TrimSpace(w.buf.String())
	w.buf.Reset()
	if line == "" || w.program == nil {
		return
	}
	if strings.HasPrefix(line, "progress:") {
		w.program.Send(installProgressMsg{text: strings.TrimSpace(strings.TrimPrefix(line, "progress:"))})
		return
	}
	w.program.Send(installLogLineMsg{text: line})
}

func soloInstallViewportLines(writer io.Writer) int {
	file, ok := writer.(*os.File)
	if !ok {
		return defaultSoloInstallViewportLines
	}
	_, height, err := term.GetSize(int(file.Fd()))
	if err != nil || height <= 0 {
		return defaultSoloInstallViewportLines
	}
	return soloInstallViewportLinesForHeight(height)
}

func soloInstallViewportLinesForHeight(height int) int {
	lines := height - 6
	if lines < minSoloInstallViewportLines {
		return minSoloInstallViewportLines
	}
	if lines > maxSoloInstallViewportLines {
		return maxSoloInstallViewportLines
	}
	return lines
}

func soloInstallViewportWidth(writer io.Writer) int {
	file, ok := writer.(*os.File)
	if !ok {
		return defaultSoloInstallViewportWidth
	}
	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil || width <= 0 {
		return defaultSoloInstallViewportWidth
	}
	return width
}
