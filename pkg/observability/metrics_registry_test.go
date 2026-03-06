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
