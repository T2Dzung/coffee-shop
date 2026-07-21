package main

import "testing"

func TestManagerOptionsEnableStableNamespacedLeaderElection(t *testing.T) {
	opts := managerOptions(":18080", ":18081", "custom-system")

	if !opts.LeaderElection {
		t.Fatal("leader election must be enabled")
	}
	if opts.LeaderElectionID != "platform-ownership-guard-leader" {
		t.Fatalf("unexpected leader election ID: %q", opts.LeaderElectionID)
	}
	if opts.LeaderElectionNamespace != "custom-system" {
		t.Fatalf("unexpected leader election namespace: %q", opts.LeaderElectionNamespace)
	}
	if opts.LeaderElectionReleaseOnCancel {
		t.Fatal("leader election lock must not be released early on cancel in v0.1")
	}
	if opts.Metrics.BindAddress != ":18080" || opts.HealthProbeBindAddress != ":18081" {
		t.Fatalf("manager endpoints were not preserved: metrics=%q probe=%q", opts.Metrics.BindAddress, opts.HealthProbeBindAddress)
	}
}
