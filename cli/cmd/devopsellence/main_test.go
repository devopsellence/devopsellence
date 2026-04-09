package main

import (
	"errors"
	"testing"

	"github.com/devopsellence/cli/internal/workflow"
)

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
