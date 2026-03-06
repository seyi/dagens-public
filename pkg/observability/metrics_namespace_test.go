package observability

import "testing"

func TestResolveMetricsNamespaceDefault(t *testing.T) {
	t.Setenv("METRICS_NAMESPACE", "")
	if got := resolveMetricsNamespace(); got != "dagens" {
		t.Fatalf("resolveMetricsNamespace() = %q, want %q", got, "dagens")
	}
}

func TestResolveMetricsNamespaceOverride(t *testing.T) {
	t.Setenv("METRICS_NAMESPACE", "custom_ns")
	if got := resolveMetricsNamespace(); got != "custom_ns" {
		t.Fatalf("resolveMetricsNamespace() = %q, want %q", got, "custom_ns")
	}
}
