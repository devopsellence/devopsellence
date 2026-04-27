package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/devopsellence/cli/internal/workflow"
)

func main() {
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	command := workflow.NewRootCommand(os.Stdin, os.Stdout, os.Stderr, mustGetwd())
	executedCommand, err := command.ExecuteContextC(rootCtx)
	if err != nil {
		var exitErr workflow.ExitError
		var renderedErr workflow.RenderedError
		code := 1
		if errors.As(err, &exitErr) {
			code = exitErr.Code
			err = exitErr.Err
		}
		if errors.Is(err, context.Canceled) {
			code = 130
		}
		if !errors.Is(err, context.Canceled) && !errors.As(err, &renderedErr) {
			operation := command.CommandPath()
			if executedCommand != nil {
				operation = executedCommand.CommandPath()
			}
			writeError(operation, code, err)
		}
		stop()
		os.Exit(code)
	}
	stop()
}

func writeError(operation string, exitCode int, err error) {
	errorObject := map[string]any{
		"code":      "command_failed",
		"message":   err.Error(),
		"exit_code": exitCode,
	}
	var structured workflow.StructuredError
	if errors.As(err, &structured) {
		for key, value := range structured.ErrorFields() {
			errorObject[key] = value
		}
	}
	payload := map[string]any{
		"ok":             false,
		"schema_version": workflow.OutputSchemaVersion,
		"operation":      operation,
		"error":          errorObject,
	}
	encoder := json.NewEncoder(os.Stderr)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if encodeErr := encoder.Encode(payload); encodeErr != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
	}
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
