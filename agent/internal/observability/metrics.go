package observability

import "github.com/prometheus/client_golang/prometheus"

type Metrics struct {
	ReconcileTotal    prometheus.Counter
	ReconcileErrors   prometheus.Counter
	ContainersCreated prometheus.Counter
	ContainersUpdated prometheus.Counter
	ContainersRemoved prometheus.Counter
}

func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		ReconcileTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "devopsellence_reconcile_total",
			Help: "Total reconcile loop executions.",
		}),
		ReconcileErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "devopsellence_reconcile_errors_total",
			Help: "Total reconcile errors.",
		}),
		ContainersCreated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "devopsellence_containers_created_total",
			Help: "Total containers created.",
		}),
		ContainersUpdated: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "devopsellence_containers_updated_total",
			Help: "Total containers updated.",
		}),
		ContainersRemoved: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "devopsellence_containers_removed_total",
			Help: "Total containers removed.",
		}),
	}

	reg.MustRegister(
		m.ReconcileTotal,
		m.ReconcileErrors,
		m.ContainersCreated,
		m.ContainersUpdated,
		m.ContainersRemoved,
	)

	return m
}
