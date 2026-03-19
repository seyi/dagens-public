package main

import "testing"

func TestHeartbeatNodeIDWithoutChurn(t *testing.T) {
	got := heartbeatNodeID(7, 1000, 0)
	if got != "worker-0007" {
		t.Fatalf("heartbeatNodeID() = %q, want %q", got, "worker-0007")
	}
}

func TestHeartbeatNodeIDWithChurn(t *testing.T) {
	got := heartbeatNodeID(65, 1000, 32)
	if got != "worker-0065-r0002" {
		t.Fatalf("heartbeatNodeID() = %q, want %q", got, "worker-0065-r0002")
	}
}
