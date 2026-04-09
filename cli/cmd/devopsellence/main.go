package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/devopsellence/cli/internal/workflow"
)

func main() {
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	command := workflow.NewRootCommand(os.Stdin, os.Stdout, os.Stderr, mustGetwd())
	if err := command.ExecuteContext(rootCtx); err != nil {
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
			fmt.Fprintln(os.Stderr, err)
		}
		stop()
		os.Exit(code)
	}
	stop()
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
