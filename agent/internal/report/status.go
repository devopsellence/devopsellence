package report

import "time"

type Phase string

const (
	PhaseReconciling Phase = "reconciling"
	PhaseSettled     Phase = "settled"
	PhaseError       Phase = "error"
)

type Status struct {
	Time       time.Time         `json:"time"`
	Revision   string            `json:"revision,omitempty"`
	Phase      Phase             `json:"phase"`
	Message    string            `json:"message,omitempty"`
	Error      string            `json:"error,omitempty"`
	Task       *TaskStatus       `json:"task,omitempty"`
	Containers []ContainerStatus `json:"containers,omitempty"`
}

type TaskStatus struct {
	Name     string `json:"name"`
	Phase    Phase  `json:"phase"`
	Message  string `json:"message,omitempty"`
	Error    string `json:"error,omitempty"`
	ExitCode int64  `json:"exit_code,omitempty"`
}

type ContainerStatus struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Hash  string `json:"hash,omitempty"`
}
