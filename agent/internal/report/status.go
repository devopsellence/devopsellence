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
	DiskCare     *DiskCareStatus     `json:"disk_care,omitempty"`
	Environments []EnvironmentStatus `json:"environments,omitempty"`
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

// DiskCareStatus reports node-local cleanup and retention state for
// devopsellence-managed Docker artifacts.
type DiskCareStatus struct {
	RetainedPreviousReleases int                `json:"retained_previous_releases"`
	RetainedReleaseCount     int                `json:"retained_release_count,omitempty"`
	LogMaxSize               string             `json:"log_max_size,omitempty"`
	LogMaxFile               int                `json:"log_max_file,omitempty"`
	LastCleanupAt            time.Time          `json:"last_cleanup_at,omitempty"`
	RemovedArtifacts         []DiskCareArtifact `json:"removed_artifacts,omitempty"`
	ReclaimedBytes           int64              `json:"reclaimed_bytes,omitempty"`
	DockerLogBytes           int64              `json:"docker_log_bytes,omitempty"`
	LastError                string             `json:"last_error,omitempty"`
}

// DiskCareArtifact describes one artifact removed by automatic disk care.
type DiskCareArtifact struct {
	Type      string `json:"type"`
	Reference string `json:"reference"`
	Reason    string `json:"reason,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
}
