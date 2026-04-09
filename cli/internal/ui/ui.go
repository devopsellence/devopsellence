package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230"))
	subtleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	okStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	errStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203"))
	cardStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("33")).Padding(0, 1)
)

type updateMsg string
type logMsg string
type doneMsg struct{}
type errMsg struct{ err error }

const maxTaskLogs = 12

type taskModel struct {
	title   string
	status  string
	logs    []string
	spinner spinner.Model
	cancel  context.CancelFunc
	done    bool
	err     error
}

func RunTask(ctx context.Context, out io.Writer, title string, fn func(context.Context, func(string), func(string)) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	taskCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	model := taskModel{
		title:   title,
		status:  "Starting…",
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		cancel:  cancel,
	}
	model.spinner.Style = subtleStyle

	program := tea.NewProgram(model, tea.WithContext(taskCtx), tea.WithOutput(out))
	taskErrs := make(chan error, 1)
	go func() {
		taskErr := fn(
			taskCtx,
			func(message string) {
				program.Send(updateMsg(message))
			},
			func(message string) {
				program.Send(logMsg(message))
			},
		)
		taskErrs <- taskErr
		if taskErr != nil {
			program.Send(errMsg{err: taskErr})
			return
		}
		program.Send(doneMsg{})
	}()

	finalModel, err := program.Run()
	if err != nil {
		cancel()
		<-taskErrs
		return err
	}
	taskErr := <-taskErrs
	result := finalModel.(taskModel)
	if result.err != nil {
		return result.err
	}
	return taskErr
}

func (m taskModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m taskModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch value := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case updateMsg:
		m.status = string(value)
		return m, nil
	case logMsg:
		m.logs = append(m.logs, string(value))
		if len(m.logs) > maxTaskLogs {
			m.logs = append([]string(nil), m.logs[len(m.logs)-maxTaskLogs:]...)
		}
		return m, nil
	case errMsg:
		m.err = value.err
		m.done = true
		return m, tea.Quit
	case doneMsg:
		m.done = true
		return m, tea.Quit
	case tea.KeyMsg:
		switch value.String() {
		case "ctrl+c", "q":
			if m.cancel != nil {
				m.cancel()
			}
			m.err = context.Canceled
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m taskModel) View() string {
	if m.done && m.err != nil {
		if errors.Is(m.err, context.Canceled) {
			return errStyle.Render("Canceled") + " " + m.title + "\n"
		}
		return errStyle.Render("Error") + " " + m.err.Error() + "\n"
	}
	if m.done {
		return okStyle.Render("Done") + " " + m.title + "\n"
	}
	view := fmt.Sprintf("%s %s\n%s\n", m.spinner.View(), titleStyle.Render(m.title), subtleStyle.Render(m.status))
	if len(m.logs) == 0 {
		return view
	}
	return view + subtleStyle.Render(strings.Join(m.logs, "\n")) + "\n"
}
