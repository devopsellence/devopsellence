package reconcile

import (
	"github.com/devopsellence/devopsellence/agent/internal/desiredstatepb"
	"github.com/devopsellence/devopsellence/agent/internal/report"
)

type IngressStatusReporter interface {
	Status(ingress *desiredstatepb.Ingress) *report.IngressStatus
}

func (r *Reconciler) IngressStatus(desired *desiredstatepb.DesiredState) *report.IngressStatus {
	if desired == nil || desired.Ingress == nil || r.opts.IngressCert == nil {
		return nil
	}
	reporter, ok := r.opts.IngressCert.(IngressStatusReporter)
	if !ok {
		return nil
	}
	return reporter.Status(desired.Ingress)
}
