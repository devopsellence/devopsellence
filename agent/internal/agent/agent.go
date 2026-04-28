package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"github.com/devopsellence/devopsellence/agent/internal/authority"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstate"
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/lifecycle"
	"github.com/devopsellence/devopsellence/agent/internal/observability"
	"github.com/devopsellence/devopsellence/agent/internal/reconcile"
	"github.com/devopsellence/devopsellence/agent/internal/report"
)

type Agent struct {
	authority              authority.Authority
	reporter               report.Reporter
	reconciler             *reconcile.Reconciler
	diagnoser              Diagnoser
	diskCare               DiskCare
	interval               time.Duration
	logger                 *slog.Logger
	metrics                *observability.Metrics
	lastReport             *reportFingerprint
	taskStore              *lifecycle.Store
	diagnoseSignalSequence int64
}

type Diagnoser interface {
	RunOnce(ctx context.Context, desiredStateSequence int64) error
}

// DiskCare performs best-effort node-local cleanup after successful reconcile.
type DiskCare interface {
	Run(ctx context.Context, desired *desiredstatepb.DesiredState) (*report.DiskCareStatus, error)
}

func New(authority authority.Authority, reconciler *reconcile.Reconciler, reporter report.Reporter, interval time.Duration, logger *slog.Logger, metrics *observability.Metrics, taskStore *lifecycle.Store) *Agent {
	return &Agent{
		authority:  authority,
		reconciler: reconciler,
		reporter:   reporter,
		interval:   interval,
		logger:     logger,
		metrics:    metrics,
		taskStore:  taskStore,
	}
}

func (a *Agent) SetDiagnoser(diagnoser Diagnoser) {
	a.diagnoser = diagnoser
}

// SetDiskCare enables best-effort automatic cleanup after successful reconcile.
func (a *Agent) SetDiskCare(diskCare DiskCare) {
	a.diskCare = diskCare
}

func (a *Agent) Run(ctx context.Context) error {
	a.logger.Info("agent started")
	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	a.tickOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			a.tickOnce(ctx)
		}
	}
}

func (a *Agent) tickOnce(ctx context.Context) {
	if err := a.reconcileOnce(ctx); err != nil {
		a.logger.Error("reconcile failed", "error", err)
	}
	if a.diagnoser != nil {
		if err := a.diagnoser.RunOnce(ctx, a.diagnoseSignalSequence); err != nil {
			a.logger.Error("diagnose failed", "error", err)
		}
	}
}

func (a *Agent) reconcileOnce(ctx context.Context) error {
	start := time.Now()
	a.metrics.ReconcileTotal.Inc()

	fetched, err := a.authority.Fetch(ctx)
	if err != nil {
		if errors.Is(err, authority.ErrNoDesiredState) {
			a.diagnoseSignalSequence = 0
			return nil
		}
		a.metrics.ReconcileErrors.Inc()
		a.reportStatus(ctx, report.Status{
			Time:  start,
			Phase: report.PhaseError,
			Error: err.Error(),
		}, 0)
		return err
	}
	a.diagnoseSignalSequence = fetched.Sequence
	desired := fetched.Desired

	if err := desiredstate.Validate(desired); err != nil {
		a.metrics.ReconcileErrors.Inc()
		a.reportStatus(ctx, report.Status{
			Time:  start,
			Phase: report.PhaseError,
			Error: err.Error(),
		}, fetched.Sequence)
		return err
	}

	for _, task := range desiredstate.RuntimeTasks(desired) {
		if err := a.ensureTaskSatisfied(ctx, fetched.Sequence, task.EnvironmentName, task.EnvironmentRevision, task.Task, true); err != nil {
			a.metrics.ReconcileErrors.Inc()
			return err
		}
	}

	result, err := a.reconciler.Reconcile(ctx, desired)
	if err != nil {
		a.metrics.ReconcileErrors.Inc()
		a.reportStatus(ctx, report.Status{
			Time:     start,
			Phase:    report.PhaseError,
			Revision: desired.Revision,
			Error:    err.Error(),
		}, fetched.Sequence)
		return err
	}

	a.metrics.ContainersCreated.Add(float64(result.Created))
	a.metrics.ContainersUpdated.Add(float64(result.Updated))
	a.metrics.ContainersRemoved.Add(float64(result.Removed))

	diskCareStatus := a.runDiskCare(ctx, desired)

	summary, environments, err := a.reconciler.CurrentStatus(ctx, desired)
	if err != nil {
		a.metrics.ReconcileErrors.Inc()
		a.reportStatus(ctx, report.Status{
			Time:     start,
			Phase:    report.PhaseError,
			Revision: desired.Revision,
			Error:    err.Error(),
		}, fetched.Sequence)
		return err
	}

	a.reportStatus(ctx, report.Status{
		Time:         start,
		Phase:        report.PhaseSettled,
		Revision:     desired.Revision,
		Message:      fmt.Sprintf("created=%d updated=%d removed=%d unchanged=%d", result.Created, result.Updated, result.Removed, result.Unchanged),
		Summary:      summary,
		DiskCare:     diskCareStatus,
		Environments: environments,
	}, fetched.Sequence)

	a.logger.Info("reconcile ok",
		"created", result.Created,
		"updated", result.Updated,
		"removed", result.Removed,
		"unchanged", result.Unchanged,
		"duration", time.Since(start).String(),
	)

	return nil
}

func (a *Agent) runDiskCare(ctx context.Context, desired *desiredstatepb.DesiredState) *report.DiskCareStatus {
	if a.diskCare == nil {
		return nil
	}
	status, err := a.diskCare.Run(ctx, desired)
	if err != nil {
		a.logger.Warn("disk care failed", "error", err)
		if status == nil {
			status = &report.DiskCareStatus{LastError: err.Error()}
		} else if status.LastError == "" {
			status.LastError = err.Error()
		}
	}
	return status
}

func (a *Agent) runTaskOnce(ctx context.Context, sequence int64, revision string, task *desiredstatepb.Task) error {
	return a.ensureTaskSatisfied(ctx, sequence, desiredstate.DefaultEnvironmentName, revision, task, false)
}

func (a *Agent) ensureTaskSatisfied(ctx context.Context, sequence int64, environmentName string, revision string, task *desiredstatepb.Task, suppressSuccessReport bool) error {
	taskHash, err := desiredstate.HashTask(task)
	if err != nil {
		a.reportStatus(ctx, report.Status{
			Time:     time.Now(),
			Revision: revision,
			Phase:    report.PhaseError,
			Error:    err.Error(),
			Task: &report.TaskStatus{
				Name:  task.GetName(),
				Phase: report.PhaseError,
				Error: err.Error(),
			},
		}, sequence)
		return err
	}
	storeName := taskStoreName(environmentName, task.GetName())
	if a.taskStore != nil && a.taskStore.Satisfied(storeName, sequence, taskHash) {
		return nil
	}

	start := time.Now()
	a.reportStatus(ctx, report.Status{
		Time:     start,
		Revision: revision,
		Phase:    report.PhaseReconciling,
		Message:  "running " + task.GetName(),
		Task: &report.TaskStatus{
			Name:    task.GetName(),
			Phase:   report.PhaseReconciling,
			Message: "running " + task.GetName(),
		},
	}, sequence)

	taskResult, err := a.reconciler.RunTask(ctx, revision, task)
	if err != nil {
		a.reportStatus(ctx, report.Status{
			Time:     start,
			Revision: revision,
			Phase:    report.PhaseError,
			Error:    err.Error(),
			Task: &report.TaskStatus{
				Name:     task.GetName(),
				Phase:    report.PhaseError,
				Error:    err.Error(),
				ExitCode: taskResult.ExitCode,
			},
		}, sequence)
		return err
	}
	if a.taskStore != nil {
		if err := a.taskStore.MarkSatisfied(storeName, sequence, taskHash); err != nil {
			a.logger.Warn("persist lifecycle task state failed", "task", task.GetName(), "error", err)
		}
	}
	if suppressSuccessReport {
		return nil
	}

	a.reportStatus(ctx, report.Status{
		Time:     start,
		Revision: revision,
		Phase:    report.PhaseSettled,
		Message:  task.GetName() + " completed",
		Task: &report.TaskStatus{
			Name:     task.GetName(),
			Phase:    report.PhaseSettled,
			Message:  task.GetName() + " completed",
			ExitCode: taskResult.ExitCode,
		},
	}, sequence)
	return nil
}

func taskStoreName(environmentName, taskName string) string {
	environmentName = strings.TrimSpace(environmentName)
	taskName = strings.TrimSpace(taskName)
	if environmentName == "" {
		return taskName
	}
	return desiredstate.ScopedKey(environmentName, taskName)
}

func (a *Agent) reportStatus(ctx context.Context, status report.Status, sequence int64) {
	fingerprint := newReportFingerprint(sequence, status)
	if a.lastReport != nil && a.lastReport.suppresses(fingerprint) {
		return
	}
	if err := a.reporter.Report(ctx, status); err != nil {
		a.logger.Warn("report status failed", "error", err, "phase", status.Phase, "revision", status.Revision, "sequence", sequence)
		return
	}
	a.lastReport = fingerprint
}

type reportFingerprint struct {
	sequence         int64
	revision         string
	phase            report.Phase
	message          string
	err              string
	taskHash         string
	diskCareHash     string
	environmentsHash string
}

func newReportFingerprint(sequence int64, status report.Status) *reportFingerprint {
	return &reportFingerprint{
		sequence:         sequence,
		revision:         status.Revision,
		phase:            status.Phase,
		message:          status.Message,
		err:              status.Error,
		taskHash:         fingerprintTask(status.Task),
		diskCareHash:     fingerprintDiskCare(status.DiskCare),
		environmentsHash: fingerprintEnvironments(status.Environments),
	}
}

func (f *reportFingerprint) suppresses(other *reportFingerprint) bool {
	if f == nil || other == nil {
		return false
	}
	if f.sequence != other.sequence || f.phase != other.phase {
		return false
	}
	if f.phase == report.PhaseSettled {
		return true
	}
	return f.revision == other.revision &&
		f.message == other.message &&
		f.err == other.err &&
		f.taskHash == other.taskHash &&
		f.diskCareHash == other.diskCareHash &&
		f.environmentsHash == other.environmentsHash
}

func fingerprintTask(task *report.TaskStatus) string {
	if task == nil {
		return ""
	}

	var builder strings.Builder
	builder.WriteString(task.Name)
	builder.WriteByte(0)
	builder.WriteString(string(task.Phase))
	builder.WriteByte(0)
	builder.WriteString(task.Message)
	builder.WriteByte(0)
	builder.WriteString(task.Error)
	builder.WriteByte(0)
	builder.WriteString(fmt.Sprintf("%d", task.ExitCode))
	return builder.String()
}

func fingerprintDiskCare(status *report.DiskCareStatus) string {
	if status == nil {
		return ""
	}
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("%d", status.RetainedPreviousReleases))
	builder.WriteByte(0)
	builder.WriteString(fmt.Sprintf("%d", status.RetainedReleaseCount))
	builder.WriteByte(0)
	builder.WriteString(status.LogMaxSize)
	builder.WriteByte(0)
	builder.WriteString(fmt.Sprintf("%d", status.LogMaxFile))
	builder.WriteByte(0)
	builder.WriteString(fmt.Sprintf("%d", status.ReclaimedBytes))
	builder.WriteByte(0)
	builder.WriteString(fmt.Sprintf("%d", status.DockerLogBytes))
	builder.WriteByte(0)
	builder.WriteString(status.LastError)
	builder.WriteByte(0)
	for _, artifact := range status.RemovedArtifacts {
		builder.WriteString(artifact.Type)
		builder.WriteByte(0)
		builder.WriteString(artifact.Reference)
		builder.WriteByte(0)
		builder.WriteString(artifact.Reason)
		builder.WriteByte(0)
		builder.WriteString(fmt.Sprintf("%d", artifact.Bytes))
		builder.WriteByte(0)
	}
	return builder.String()
}

func fingerprintEnvironments(environments []report.EnvironmentStatus) string {
	if len(environments) == 0 {
		return ""
	}

	var builder strings.Builder
	for _, environment := range environments {
		builder.WriteString(environment.Name)
		builder.WriteByte(0)
		builder.WriteString(environment.Revision)
		builder.WriteByte(0)
		builder.WriteString(string(environment.Phase))
		builder.WriteByte(0)
		for _, service := range environment.Services {
			builder.WriteString(service.Name)
			builder.WriteByte(0)
			builder.WriteString(service.Kind)
			builder.WriteByte(0)
			builder.WriteString(string(service.Phase))
			builder.WriteByte(0)
			builder.WriteString(service.Container)
			builder.WriteByte(0)
			builder.WriteString(service.State)
			builder.WriteByte(0)
			builder.WriteString(service.Health)
			builder.WriteByte(0)
			builder.WriteString(service.Hash)
			builder.WriteByte(0)
		}
	}
	return builder.String()
}
