package ui

import (
	"context"
	"errors"
	"testing"
)

func TestRunTaskExecutesFunctionDirectly(t *testing.T) {
	called := false
	err := RunTask(t.Context(), nil, "ignored", func(_ context.Context, update, log func(string)) error {
		called = true
		update("ignored")
		log("ignored")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("RunTask did not call function")
	}
}

func TestRunTaskReturnsError(t *testing.T) {
	want := errors.New("boom")
	err := RunTask(t.Context(), nil, "ignored", func(_ context.Context, _, _ func(string)) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("RunTask error = %v, want %v", err, want)
	}
}
