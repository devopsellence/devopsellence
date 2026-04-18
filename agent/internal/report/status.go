package report

import "time"

type Phase string

const (
	PhaseReconciling Phase = "reconciling"
	PhaseSettled     Phase = "settled"
	PhaseError       Phase = "error"
)

type Status struct {
	Time         time.Time           `json:"time"`
	Revision     string              `json:"revision,omitempty"`
	Phase        Phase               `json:"phase"`
	Message      string              `json:"message,omitempty"`
	Error        string              `json:"error,omitempty"`
	Summary      *Summary            `json:"summary,omitempty"`
	Task         *TaskStatus         `json:"task,omitempty"`
	Environments []EnvironmentStatus `json:"environments,omitempty"`
	Containers   []ContainerStatus   `json:"containers,omitempty"`
}

type Summary struct {
	Environments      int `json:"environments,omitempty"`
	Services          int `json:"services,omitempty"`
	UnhealthyServices int `json:"unhealthy_services,omitempty"`
}

type EnvironmentStatus struct {
	Name     string          `json:"name"`
	Revision string          `json:"revision,omitempty"`
	Phase    Phase           `json:"phase,omitempty"`
	Services []ServiceStatus `json:"services,omitempty"`
}

type ServiceStatus struct {
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"`
	Phase     Phase  `json:"phase,omitempty"`
	Container string `json:"container,omitempty"`
	State     string `json:"state,omitempty"`
	Health    string `json:"health,omitempty"`
	Hash      string `json:"hash,omitempty"`
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
