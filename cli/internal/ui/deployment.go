package ui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type DeploymentSummary struct {
	AssignedNodes int
	Pending       int
	Reconciling   int
	Settled       int
	Error         int
	Complete      bool
	Failed        bool
}

type DeploymentNode struct {
	Name       string
	Phase      string
	Detail     string
	ReportedAt string
}

type DeploymentSnapshot struct {
	Project       string
	Environment   string
	Revision      string
	PublicURL     string
	Status        string
	StatusMessage string
	Summary       DeploymentSummary
	Nodes         []DeploymentNode
}

type deploymentSnapshotMsg struct {
	snapshot DeploymentSnapshot
	err      error
}

type deploymentPollMsg struct{}

type deploymentMonitorModel struct {
	ctx      context.Context
	title    string
	interval time.Duration
	fetch    func(context.Context) (DeploymentSnapshot, error)
	renderer Renderer
	spinner  spinner.Model
	snapshot DeploymentSnapshot
	done     bool
	err      error
}

func MonitorDeployment(ctx context.Context, out io.Writer, title string, interval time.Duration, fetch func(context.Context) (DeploymentSnapshot, error)) (DeploymentSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}

	spin := spinner.New()
	spin.Spinner = spinner.Dot
	spin.Style = DefaultTheme().Accent

	model := deploymentMonitorModel{
		ctx:      ctx,
		title:    strings.TrimSpace(title),
		interval: interval,
		fetch:    fetch,
		renderer: DefaultRenderer(),
		spinner:  spin,
	}

	program := tea.NewProgram(model, tea.WithOutput(out), tea.WithInput(nil), tea.WithContext(ctx))
	finalModel, err := program.Run()
	if err != nil {
		return DeploymentSnapshot{}, err
	}

	completed, ok := finalModel.(deploymentMonitorModel)
	if !ok {
		return DeploymentSnapshot{}, fmt.Errorf("unexpected deployment monitor type %T", finalModel)
	}
	if completed.err != nil {
		return completed.snapshot, completed.err
	}
	return completed.snapshot, nil
}

func (m deploymentMonitorModel) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.fetchCmd())
}

func (m deploymentMonitorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := msg.(type) {
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(typed)
		return m, cmd
	case deploymentSnapshotMsg:
		if typed.err != nil {
			m.err = typed.err
			m.done = true
			return m, tea.Quit
		}
		m.snapshot = typed.snapshot
		if m.snapshot.Summary.Complete || m.snapshot.Summary.Failed {
			m.done = true
			return m, tea.Quit
		}
		return m, tea.Tick(m.interval, func(time.Time) tea.Msg { return deploymentPollMsg{} })
	case deploymentPollMsg:
		return m, m.fetchCmd()
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

func (m deploymentMonitorModel) View() string {
	lines := []string{m.renderer.Title(m.title), ""}

	if m.snapshot.Revision != "" || m.snapshot.Environment != "" || m.snapshot.Project != "" {
		header := ""
		if m.snapshot.Project != "" {
			header += "Project: " + m.snapshot.Project + "   "
		}
		header += "Environment: " + m.snapshot.Environment + "   Revision: " + m.snapshot.Revision
		lines = append(lines, m.renderer.Muted(header))
	}
	summary := fmt.Sprintf("Nodes %d   Pending %d   Reconciling %d   Settled %d   Error %d",
		m.snapshot.Summary.AssignedNodes,
		m.snapshot.Summary.Pending,
		m.snapshot.Summary.Reconciling,
		m.snapshot.Summary.Settled,
		m.snapshot.Summary.Error,
	)
	lines = append(lines, m.renderer.Muted(summary))
	if strings.TrimSpace(m.snapshot.StatusMessage) != "" {
		lines = append(lines, m.renderer.Muted("Status: "+m.snapshot.StatusMessage))
	}
	if m.snapshot.PublicURL != "" {
		lines = append(lines, m.renderer.Muted("URL: "+m.snapshot.PublicURL))
	}
	lines = append(lines, "")

	for _, node := range m.snapshot.Nodes {
		lines = append(lines, m.renderNode(node))
	}

	if m.done {
		lines = append(lines, "")
		if m.err != nil {
			lines = append(lines, m.renderer.Error(m.err.Error()))
		} else if m.snapshot.Summary.Failed {
			lines = append(lines, m.renderer.Error("Deploy failed"))
		} else {
			lines = append(lines, m.renderer.Success("Deploy complete"))
		}
	}

	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func (m deploymentMonitorModel) renderNode(node DeploymentNode) string {
	detail := strings.TrimSpace(node.Detail)
	if node.ReportedAt != "" {
		if detail == "" {
			detail = node.ReportedAt
		} else {
			detail += "  " + node.ReportedAt
		}
	}
	switch node.Phase {
	case "settled":
		return m.renderer.Success(node.Name + detailText(detail))
	case "error":
		return m.renderer.Error(node.Name + detailText(detail))
	case "reconciling":
		return m.spinner.View() + " " + m.renderer.Accent(node.Name+detailText(detail))
	case "pending":
		return m.spinner.View() + " " + m.renderer.Accent(node.Name+detailText(detail))
	default:
		return "[ ] " + node.Name + detailText(detail)
	}
}

func detailText(detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return ""
	}
	return " - " + detail
}

func (m deploymentMonitorModel) fetchCmd() tea.Cmd {
	return func() tea.Msg {
		snapshot, err := m.fetch(m.ctx)
		return deploymentSnapshotMsg{snapshot: snapshot, err: err}
	}
}
