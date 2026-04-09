package app

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/devopsellence/cli/internal/workflow"
)

func Run() error {
	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command := workflow.NewRootCommand(os.Stdin, os.Stdout, os.Stderr, mustGetwd())
	if err := command.ExecuteContext(rootCtx); err != nil {
		var exitErr workflow.ExitError
		if errors.As(err, &exitErr) {
			if errors.Is(exitErr.Err, context.Canceled) {
				return WithExitCode(exitErr.Err, 130)
			}
			return WithExitCode(exitErr.Err, exitErr.Code)
		}
		if errors.Is(err, context.Canceled) {
			return WithExitCode(err, 130)
		}
		return err
	}
	return nil
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}
