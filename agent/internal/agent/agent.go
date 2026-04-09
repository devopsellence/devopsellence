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

	if desired.ReleaseCommand != nil {
		if err := a.ensureTaskSatisfied(ctx, fetched.Sequence, desired.Revision, desired.ReleaseCommand, true); err != nil {
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

	a.reportStatus(ctx, report.Status{
		Time:     start,
		Phase:    report.PhaseSettled,
		Revision: desired.Revision,
		Message:  fmt.Sprintf("created=%d updated=%d removed=%d unchanged=%d", result.Created, result.Updated, result.Removed, result.Unchanged),
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

func (a *Agent) runTaskOnce(ctx context.Context, sequence int64, revision string, task *desiredstatepb.Task) error {
	return a.ensureTaskSatisfied(ctx, sequence, revision, task, false)
}

func (a *Agent) ensureTaskSatisfied(ctx context.Context, sequence int64, revision string, task *desiredstatepb.Task, suppressSuccessReport bool) error {
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
	if a.taskStore != nil && a.taskStore.Satisfied(task.GetName(), sequence, taskHash) {
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
		if err := a.taskStore.MarkSatisfied(task.GetName(), sequence, taskHash); err != nil {
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
	sequence       int64
	revision       string
	phase          report.Phase
	message        string
	err            string
	taskHash       string
	containersHash string
}

func newReportFingerprint(sequence int64, status report.Status) *reportFingerprint {
	return &reportFingerprint{
		sequence:       sequence,
		revision:       status.Revision,
		phase:          status.Phase,
		message:        status.Message,
		err:            status.Error,
		taskHash:       fingerprintTask(status.Task),
		containersHash: fingerprintContainers(status.Containers),
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
		f.containersHash == other.containersHash
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

func fingerprintContainers(containers []report.ContainerStatus) string {
	if len(containers) == 0 {
		return ""
	}

	var builder strings.Builder
	for _, container := range containers {
		builder.WriteString(container.Name)
		builder.WriteByte(0)
		builder.WriteString(container.State)
		builder.WriteByte(0)
		builder.WriteString(container.Hash)
		builder.WriteByte(0)
	}
	return builder.String()
}
