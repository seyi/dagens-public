package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestNewMetricsWithRegistrySupportsIsolatedRegistries(t *testing.T) {
	regA := prometheus.NewRegistry()
	regB := prometheus.NewRegistry()

	metricsA := NewMetricsWithRegistry("dagens_test", regA)
	metricsB := NewMetricsWithRegistry("dagens_test", regB)

	metricsA.RecordAgentExecution("agent-a", "type-a", "success", 0)
	metricsB.RecordAgentExecution("agent-b", "type-b", "success", 0)

	if _, err := regA.Gather(); err != nil {
		t.Fatalf("regA gather failed: %v", err)
	}
	if _, err := regB.Gather(); err != nil {
		t.Fatalf("regB gather failed: %v", err)
	}
}

func TestRecordAgentErrorUsesUnknownAgentType(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetricsWithRegistry("dagens_test", reg)

	metrics.RecordAgentError("agent-a", "timeout")

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}

	found := false
	for _, fam := range families {
		if fam.GetName() != "dagens_test_execution_errors_total" {
			continue
		}
		for _, metric := range fam.GetMetric() {
			labels := map[string]string{}
			for _, lp := range metric.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["agent_name"] == "agent-a" &&
				labels["agent_type"] == "unknown" &&
				labels["error_type"] == "timeout" {
				found = true
				break
			}
		}
	}

	if !found {
		t.Fatal("expected execution error sample with agent_type=unknown")
	}
}

func TestRecordWorkerDispatchRejectionPublishesReasonLabel(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetricsWithRegistry("dagens_test", reg)

	metrics.RecordWorkerDispatchRejection("stale_epoch")

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}

	found := false
	for _, fam := range families {
		if fam.GetName() != "dagens_test_worker_dispatch_rejections_total" {
			continue
		}
		for _, metric := range fam.GetMetric() {
			labels := map[string]string{}
			for _, lp := range metric.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			if labels["reason"] == "stale_epoch" && metric.GetCounter().GetValue() == 1 {
				found = true
				break
			}
		}
	}

	if !found {
		t.Fatal("expected worker dispatch rejection sample with reason=stale_epoch")
	}
}

func TestRecordSchedulerReconcileSucceededPublishesModeLabel(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetricsWithRegistry("dagens_test", reg)

	metrics.RecordSchedulerReconcileSucceeded("follower")

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}

	foundCounter := false
	foundGauge := false
	for _, fam := range families {
		switch fam.GetName() {
		case "dagens_test_scheduler_reconcile_runs_total":
			for _, metric := range fam.GetMetric() {
				labels := map[string]string{}
				for _, lp := range metric.GetLabel() {
					labels[lp.GetName()] = lp.GetValue()
				}
				if labels["mode"] == "follower" && metric.GetCounter().GetValue() == 1 {
					foundCounter = true
					break
				}
			}
		case "dagens_test_scheduler_last_reconcile_timestamp_seconds":
			for _, metric := range fam.GetMetric() {
				if metric.GetGauge().GetValue() > 0 {
					foundGauge = true
					break
				}
			}
		}
	}

	if !foundCounter {
		t.Fatal("expected scheduler reconcile counter sample with mode=follower")
	}
	if !foundGauge {
		t.Fatal("expected scheduler last reconcile timestamp gauge to be updated")
	}
}
