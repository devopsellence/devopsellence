package workflow

type RenderedError struct {
	Err error
}

// StructuredError lets command errors add machine-readable fields to the
// standard JSON error object rendered by the CLI entrypoint.
type StructuredError interface {
	ErrorFields() map[string]any
}

func (e RenderedError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e RenderedError) Unwrap() error {
	return e.Err
}
