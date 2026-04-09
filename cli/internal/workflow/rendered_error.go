package workflow

type RenderedError struct {
	Err error
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
