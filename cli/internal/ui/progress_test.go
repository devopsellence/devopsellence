package ui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func TestProgressModelAdvancesAcrossCompletedSteps(t *testing.T) {
	model := progressModel{
		title:    "Deploying",
		renderer: DefaultRenderer(),
		spinner:  spinner.New(),
		steps: []Step{
			{Title: "Discover"},
			{Title: "Publish"},
		},
		results: make([]StepResult, 2),
	}

	updated, _ := model.Update(stepDoneMsg{index: 0, detail: "workspace ready"})
	got := updated.(progressModel)

	if got.current != 1 {
		t.Fatalf("expected current step 1, got %d", got.current)
	}
	if got.results[0].Detail != "workspace ready" {
		t.Fatalf("expected step detail to be recorded, got %q", got.results[0].Detail)
	}
}

func TestProgressModelStopsOnError(t *testing.T) {
	model := progressModel{
		title:    "Deploying",
		renderer: DefaultRenderer(),
		spinner:  spinner.New(),
		steps:    []Step{{Title: "Publish"}},
		results:  make([]StepResult, 1),
	}

	updated, cmd := model.Update(stepDoneMsg{index: 0, err: errors.New("boom")})
	got := updated.(progressModel)

	if got.err == nil || got.err.Error() != "boom" {
		t.Fatalf("expected error to be recorded, got %v", got.err)
	}
	if !got.done {
		t.Fatal("expected model to be marked done")
	}
	if cmd == nil {
		t.Fatal("expected quit command on failure")
	}
}

func TestProgressModelViewShowsStates(t *testing.T) {
	model := progressModel{
		title:    "Deploying",
		renderer: DefaultRenderer(),
		spinner:  spinner.New(),
		steps: []Step{
			{Title: "Discover"},
			{Title: "Publish"},
		},
		results: []StepResult{
			{Title: "Discover", Detail: "workspace ready"},
			{},
		},
		current: 1,
	}

	view := model.View()
	for _, expected := range []string{"Deploying", "Discover", "workspace ready", "Publish"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("expected %q in view %q", expected, view)
		}
	}
}

func TestRunStepsReturnsStepResults(t *testing.T) {
	results, err := Runner{Title: "Checking", Renderer: DefaultRenderer()}.Run(context.Background(), ioDiscard{}, []Step{
		{
			Title: "First",
			Action: func(context.Context) (string, error) {
				return "done", nil
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Detail != "done" {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestProgressModelHandlesCancelKey(t *testing.T) {
	model := progressModel{
		renderer: DefaultRenderer(),
		spinner:  spinner.New(),
		steps:    []Step{{Title: "Step"}},
		results:  make([]StepResult, 1),
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(progressModel)

	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("expected cancel error, got %v", got.err)
	}
	if !got.done {
		t.Fatal("expected model done on cancel")
	}
	if cmd == nil {
		t.Fatal("expected quit command on cancel")
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}
