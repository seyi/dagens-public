package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/seyi/dagens/pkg/observability"
	"github.com/seyi/dagens/pkg/retention"
)

func TestRunTransitionRetentionMetricsPollerPublishesSnapshot(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := observability.NewMetricsWithRegistry("dagens_test", reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls int
	wait := runTransitionRetentionMetricsPoller(
		ctx,
		time.Hour,
		time.Second,
		metrics,
		func(context.Context) (retention.VisibilitySnapshot, error) {
			calls++
			return retention.VisibilitySnapshot{
				UnfinishedJobs: 2,
				TerminalBuckets: []retention.TerminalAgeBucket{
					{Label: "lt_7d", Jobs: 9},
				},
				EligibleArchive: retention.EligibleArchiveStats{
					Jobs:           4,
					TransitionRows: 12,
					TaskRows:       3,
					SequenceRows:   1,
				},
			}, nil
		},
		nil,
	)
	cancel()
	wait()

	if calls != 1 {
		t.Fatalf("expected one immediate refresh call, got %d", calls)
	}

	assertMetricValue(t, reg, "dagens_test_transition_retention_unfinished_jobs", nil, 2)
	assertMetricValue(t, reg, "dagens_test_transition_retention_terminal_jobs", map[string]string{"bucket": "lt_7d"}, 9)
	assertMetricValue(t, reg, "dagens_test_transition_retention_eligible_jobs", nil, 4)
	assertMetricValue(t, reg, "dagens_test_transition_retention_eligible_rows", map[string]string{"table": "scheduler_job_transitions"}, 12)
}

func TestRunTransitionRetentionMetricsPollerRecordsFailuresAndRecoversPanic(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := observability.NewMetricsWithRegistry("dagens_test", reg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	wait := runTransitionRetentionMetricsPoller(
		ctx,
		10*time.Millisecond,
		time.Second,
		metrics,
		func(context.Context) (retention.VisibilitySnapshot, error) {
			calls++
			switch calls {
			case 1:
				return retention.VisibilitySnapshot{}, errors.New("db unavailable")
			case 2:
				panic("boom")
			default:
				return retention.VisibilitySnapshot{UnfinishedJobs: 1}, nil
			}
		},
		nil,
	)

	time.Sleep(35 * time.Millisecond)
	cancel()
	wait()

	assertMetricValueAtLeast(t, reg, "dagens_test_transition_retention_refresh_failures_total", nil, 2)
	assertMetricValue(t, reg, "dagens_test_transition_retention_unfinished_jobs", nil, 1)
}

func assertMetricValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string, expected float64) {
	t.Helper()
	value, found := gatherMetricValue(t, reg, name, labels)
	if !found {
		t.Fatalf("metric %s with labels %v not found", name, labels)
	}
	if value != expected {
		t.Fatalf("metric %s with labels %v = %v, want %v", name, labels, value, expected)
	}
}

func assertMetricValueAtLeast(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string, min float64) {
	t.Helper()
	value, found := gatherMetricValue(t, reg, name, labels)
	if !found {
		t.Fatalf("metric %s with labels %v not found", name, labels)
	}
	if value < min {
		t.Fatalf("metric %s with labels %v = %v, want >= %v", name, labels, value, min)
	}
}

func gatherMetricValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather failed: %v", err)
	}
	for _, fam := range families {
		if fam.GetName() != name {
			continue
		}
		for _, metric := range fam.GetMetric() {
			actualLabels := map[string]string{}
			for _, lp := range metric.GetLabel() {
				actualLabels[lp.GetName()] = lp.GetValue()
			}
			if !labelsMatch(labels, actualLabels) {
				continue
			}
			if metric.Gauge != nil {
				return metric.GetGauge().GetValue(), true
			}
			if metric.Counter != nil {
				return metric.GetCounter().GetValue(), true
			}
		}
	}
	return 0, false
}

func labelsMatch(expected, actual map[string]string) bool {
	if len(expected) == 0 {
		return len(actual) == 0
	}
	for k, v := range expected {
		if actual[k] != v {
			return false
		}
	}
	return true
}
