package main

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/devopsellence/cli/internal/workflow"
)

type structuredTestError struct{}

func (structuredTestError) Error() string { return "structured failure" }

func (structuredTestError) ErrorFields() map[string]any {
	return map[string]any{
		"message":    "do not override standard message",
		"next_steps": []string{"devopsellence status"},
	}
}

func TestWriteErrorIncludesStructuredFields(t *testing.T) {
	originalStderr := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Stderr = originalStderr })
	os.Stderr = writer

	writeError("devopsellence deploy", 1, structuredTestError{})
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	var payload map[string]any
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	errorPayload := payload["error"].(map[string]any)
	if errorPayload["message"] != "structured failure" {
		t.Fatalf("message = %#v, want standard error message", errorPayload["message"])
	}
	steps := errorPayload["next_steps"].([]any)
	if len(steps) != 1 || steps[0] != "devopsellence status" {
		t.Fatalf("next_steps = %#v, want structured field", steps)
	}
}

func TestRenderedErrorDoesNotNeedExtraPrint(t *testing.T) {
	err := workflow.ExitError{
		Code: 1,
		Err:  workflow.RenderedError{Err: errors.New("already rendered")},
	}

	var exitErr workflow.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatal("expected wrapped exit error")
	}

	var renderedErr workflow.RenderedError
	if !errors.As(exitErr.Err, &renderedErr) {
		t.Fatal("expected rendered error marker")
	}
}
