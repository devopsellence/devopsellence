package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunStepsExecutesSequentially(t *testing.T) {
	var calls []string
	results, err := RunSteps(t.Context(), nil, "ignored", []Step{
		{Title: "first", Action: func(context.Context) (string, error) { calls = append(calls, "first"); return "ok", nil }},
		{Title: "second", Action: func(context.Context) (string, error) { calls = append(calls, "second"); return "done", nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(calls, ","), "first,second"; got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	if len(results) != 2 || results[0].Detail != "ok" || results[1].Detail != "done" {
		t.Fatalf("results = %#v", results)
	}
}

func TestRunStepsStopsOnError(t *testing.T) {
	want := errors.New("boom")
	results, err := RunSteps(t.Context(), nil, "ignored", []Step{
		{Title: "first", Action: func(context.Context) (string, error) { return "", want }},
		{Title: "second", Action: func(context.Context) (string, error) { t.Fatal("second step should not run"); return "", nil }},
	})
	if !errors.Is(err, want) {
		t.Fatalf("RunSteps error = %v, want %v", err, want)
	}
	if len(results) != 2 || !errors.Is(results[0].Err, want) {
		t.Fatalf("results = %#v", results)
	}
}
