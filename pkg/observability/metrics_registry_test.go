package observability

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/seyi/dagens/pkg/retention"
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

func TestSetTransitionRetentionSnapshotPublishesVisibilityGauges(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := NewMetricsWithRegistry("dagens_test", reg)

	metrics.SetTransitionRetentionSnapshot(retention.VisibilitySnapshot{
		UnfinishedJobs: 3,
		TerminalBuckets: []retention.TerminalAgeBucket{
			{Label: "lt_7d", Jobs: 7},
			{Label: "gte_90d", Jobs: 2},
		},
		EligibleArchive: retention.EligibleArchiveStats{
			HotRetentionDays: 14,
			Jobs:             5,
			TransitionRows:   40,
			TaskRows:         12,
			SequenceRows:     5,
		},
	})

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}

	foundUnfinished := false
	foundBucket := false
	foundEligibleJobs := false
	foundEligibleRows := false
	foundRefresh := false
	for _, fam := range families {
		switch fam.GetName() {
		case "dagens_test_transition_retention_unfinished_jobs":
			for _, metric := range fam.GetMetric() {
				if metric.GetGauge().GetValue() == 3 {
					foundUnfinished = true
				}
			}
		case "dagens_test_transition_retention_terminal_jobs":
			for _, metric := range fam.GetMetric() {
				labels := map[string]string{}
				for _, lp := range metric.GetLabel() {
					labels[lp.GetName()] = lp.GetValue()
				}
				if labels["bucket"] == "lt_7d" && metric.GetGauge().GetValue() == 7 {
					foundBucket = true
				}
			}
		case "dagens_test_transition_retention_eligible_jobs":
			for _, metric := range fam.GetMetric() {
				if metric.GetGauge().GetValue() == 5 {
					foundEligibleJobs = true
				}
			}
		case "dagens_test_transition_retention_eligible_rows":
			for _, metric := range fam.GetMetric() {
				labels := map[string]string{}
				for _, lp := range metric.GetLabel() {
					labels[lp.GetName()] = lp.GetValue()
				}
				if labels["table"] == "scheduler_job_transitions" && metric.GetGauge().GetValue() == 40 {
					foundEligibleRows = true
				}
			}
		case "dagens_test_transition_retention_last_refresh_timestamp_seconds":
			for _, metric := range fam.GetMetric() {
				if metric.GetGauge().GetValue() > 0 {
					foundRefresh = true
				}
			}
		}
	}

	if !foundUnfinished {
		t.Fatal("expected unfinished jobs gauge to be published")
	}
	if !foundBucket {
		t.Fatal("expected terminal bucket gauge to be published")
	}
	if !foundEligibleJobs {
		t.Fatal("expected eligible jobs gauge to be published")
	}
	if !foundEligibleRows {
		t.Fatal("expected eligible rows gauge to be published")
	}
	if !foundRefresh {
		t.Fatal("expected retention refresh timestamp gauge to be published")
	}
}
