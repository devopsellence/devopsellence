package ui

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

func TestTaskModelHandlesCancelKey(t *testing.T) {
	canceled := false
	model := taskModel{
		title:   "Deploy",
		spinner: spinner.New(),
		cancel: func() {
			canceled = true
		},
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(taskModel)

	if !canceled {
		t.Fatal("expected cancel func to be called")
	}
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

func TestTaskModelStoresRecentLogs(t *testing.T) {
	model := taskModel{
		title:   "Deploy",
		spinner: spinner.New(),
	}

	for i := 0; i < maxTaskLogs+3; i++ {
		updated, _ := model.Update(logMsg("line"))
		model = updated.(taskModel)
	}

	if len(model.logs) != maxTaskLogs {
		t.Fatalf("expected %d logs, got %d", maxTaskLogs, len(model.logs))
	}
}
