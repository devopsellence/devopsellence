package ui

import (
	"context"
	"io"
	"strings"
)

type Step struct {
	Title  string
	Action func(context.Context) (string, error)
}

type StepResult struct {
	Title  string
	Detail string
	Err    error
}

type Runner struct {
	Title    string
	Renderer Renderer
}

func RunSteps(ctx context.Context, out io.Writer, title string, steps []Step) ([]StepResult, error) {
	return Runner{Title: title, Renderer: DefaultRenderer()}.Run(ctx, out, steps)
}

func (r Runner) Run(ctx context.Context, _ io.Writer, steps []Step) ([]StepResult, error) {
	if len(steps) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	results := make([]StepResult, len(steps))
	for index, step := range steps {
		result := StepResult{Title: strings.TrimSpace(step.Title)}
		if step.Action != nil {
			detail, err := step.Action(ctx)
			result.Detail = strings.TrimSpace(detail)
			result.Err = err
			results[index] = result
			if err != nil {
				return results, err
			}
			continue
		}
		results[index] = result
	}
	return results, nil
}
