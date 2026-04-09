package ui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type Step struct {
	Title  string
	Action func(context.Context) (string, error)
}

type StepResult struct {
	Title  string
	Detail string
	Err    error
}

type Runner struct {
	Title    string
	Renderer Renderer
}

type stepDoneMsg struct {
	index  int
	detail string
	err    error
}

type progressModel struct {
	title    string
	ctx      context.Context
	renderer Renderer
	spinner  spinner.Model
	steps    []Step
	results  []StepResult
	current  int
	done     bool
	err      error
}

func RunSteps(ctx context.Context, out io.Writer, title string, steps []Step) ([]StepResult, error) {
	return Runner{Title: title, Renderer: DefaultRenderer()}.Run(ctx, out, steps)
}

func (r Runner) Run(ctx context.Context, out io.Writer, steps []Step) ([]StepResult, error) {
	if len(steps) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	renderer := r.Renderer.normalized()

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = DefaultTheme().Accent

	model := progressModel{
		title:    strings.TrimSpace(r.Title),
		ctx:      ctx,
		renderer: renderer,
		spinner:  spin,
		steps:    steps,
		results:  make([]StepResult, len(steps)),
	}

	program := tea.NewProgram(model, tea.WithOutput(out), tea.WithInput(nil), tea.WithContext(ctx))
	finalModel, err := program.Run()
	if err != nil {
		return nil, err
	}

	completed, ok := finalModel.(progressModel)
	if !ok {
		return nil, fmt.Errorf("unexpected progress model type %T", finalModel)
	}

	if completed.err != nil {
		return completed.results, completed.err
	}
	return completed.results, nil
}

func (m progressModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.startStep(0))
}

func (m progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(typed)
		return m, cmd
	case stepDoneMsg:
		m.results[typed.index] = StepResult{
			Title:  m.steps[typed.index].Title,
			Detail: typed.detail,
			Err:    typed.err,
		}
		if typed.err != nil {
			m.err = typed.err
			m.done = true
			return m, tea.Quit
		}
		next := typed.index + 1
		if next >= len(m.steps) {
			m.done = true
			return m, tea.Quit
		}
		m.current = next
		return m, m.startStep(next)
	case tea.KeyMsg:
		switch typed.String() {
		case "ctrl+c", "q":
			m.err = context.Canceled
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m progressModel) View() string {
	lines := make([]string, 0, len(m.steps)+2)
	if m.title != "" {
		lines = append(lines, m.renderer.Title(m.title))
		lines = append(lines, "")
	}

	for index, step := range m.steps {
		lines = append(lines, m.renderStep(index, step))
	}

	if m.done && m.err == nil {
		lines = append(lines, "")
		lines = append(lines, m.renderer.Success("Complete"))
	}

	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func (m progressModel) renderStep(index int, step Step) string {
	title := strings.TrimSpace(step.Title)
	result := m.results[index]

	switch {
	case result.Err != nil:
		return m.renderer.Error(title + detailSuffix(result.Detail, result.Err))
	case result.Detail != "":
		return m.renderer.Success(title + " - " + result.Detail)
	case m.done && index > m.current:
		return "[ ] " + title
	case index == m.current && !m.done:
		return m.spinner.View() + " " + m.renderer.Accent(title)
	case index < m.current:
		return m.renderer.Success(title)
	default:
		return "[ ] " + title
	}
}

func detailSuffix(detail string, err error) string {
	detail = strings.TrimSpace(detail)
	if detail != "" {
		return " - " + detail
	}
	if err == nil {
		return ""
	}
	return " - " + err.Error()
}

func (m progressModel) startStep(index int) tea.Cmd {
	step := m.steps[index]
	return func() tea.Msg {
		if step.Action == nil {
			return stepDoneMsg{index: index}
		}
		detail, err := step.Action(m.ctx)
		return stepDoneMsg{index: index, detail: detail, err: err}
	}
}
