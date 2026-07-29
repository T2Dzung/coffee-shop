package prod

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

type probeRunner struct {
	requests []command.Request
}

func (r *probeRunner) Run(_ context.Context, request command.Request) (command.Result, error) {
	r.requests = append(r.requests, request)
	for index, argument := range request.Args {
		if argument != "--request" || index+1 >= len(request.Args) {
			continue
		}
		switch request.Args[index+1] {
		case "GET":
			return command.Result{Stdout: `{"itemTypes":[{"type":0}]}`}, nil
		case "POST":
			return command.Result{Stdout: `{"orderId":"test"}`}, nil
		}
	}
	return command.Result{}, nil
}

func TestActiveApplicationLoadBalancerWaitsForAWSActiveState(t *testing.T) {
	t.Parallel()
	document := loadBalancersDocument{}
	document.LoadBalancers = append(document.LoadBalancers, struct {
		ARN   string `json:"LoadBalancerArn"`
		DNS   string `json:"DNSName"`
		Type  string `json:"Type"`
		State struct {
			Code string `json:"Code"`
		} `json:"State"`
		AvailabilityZones []struct {
			Zone string `json:"ZoneName"`
		} `json:"AvailabilityZones"`
	}{ARN: "arn:alb", DNS: "example.elb.amazonaws.com", Type: "application"})
	document.LoadBalancers[0].State.Code = "provisioning"

	arn, zones, ready, err := activeApplicationLoadBalancer(document, "example.elb.amazonaws.com")
	require.NoError(t, err)
	require.False(t, ready)
	require.Empty(t, arn)
	require.Empty(t, zones)

	document.LoadBalancers[0].State.Code = "active"
	document.LoadBalancers[0].AvailabilityZones = append(document.LoadBalancers[0].AvailabilityZones,
		struct {
			Zone string `json:"ZoneName"`
		}{Zone: "ap-southeast-1a"},
		struct {
			Zone string `json:"ZoneName"`
		}{Zone: "ap-southeast-1b"},
	)

	arn, zones, ready, err = activeApplicationLoadBalancer(document, "example.elb.amazonaws.com")
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "arn:alb", arn)
	require.ElementsMatch(t, []string{"ap-southeast-1a", "ap-southeast-1b"}, zones)
}

func TestTransactionProbeRequiredOnlyForMutatingLifecycleActions(t *testing.T) {
	t.Parallel()
	require.True(t, transactionProbeRequired(ActionSetup))
	require.True(t, transactionProbeRequired(ActionReconcile))
	require.False(t, transactionProbeRequired(ActionStatus))
	require.False(t, transactionProbeRequired(ActionResilience))
}

func TestGoldenJourneyStatusIsReadOnly(t *testing.T) {
	t.Parallel()
	runner := &probeRunner{}
	operations := &RealOperations{Runner: runner}

	require.NoError(t, operations.verifyGoldenJourney(context.Background(), "http://example.test", false))
	require.Len(t, runner.requests, 1)
	require.Contains(t, runner.requests[0].Args, "GET")
	require.NotContains(t, runner.requests[0].Args, "POST")
}

func TestGoldenJourneyLifecycleVerificationKeepsWriteProbe(t *testing.T) {
	t.Parallel()
	runner := &probeRunner{}
	operations := &RealOperations{Runner: runner}

	require.NoError(t, operations.verifyGoldenJourney(context.Background(), "http://example.test", true))
	require.Len(t, runner.requests, 2)
	require.Contains(t, runner.requests[0].Args, "GET")
	require.Contains(t, runner.requests[1].Args, "POST")
}

func TestTargetGroupForServiceRejectsMissingAndAmbiguousCoverage(t *testing.T) {
	t.Parallel()
	document := targetGroupsDocument{}
	arn, path, err := targetGroupForService(document, "proxy", 5000)
	require.ErrorContains(t, err, "no IP target group")
	require.Empty(t, arn)
	require.Empty(t, path)

	document.TargetGroups = append(document.TargetGroups,
		struct {
			ARN             string `json:"TargetGroupArn"`
			TargetType      string `json:"TargetType"`
			Port            int    `json:"Port"`
			HealthCheckPath string `json:"HealthCheckPath"`
		}{ARN: "arn:first", TargetType: "ip", Port: 5000, HealthCheckPath: "/healthz"},
		struct {
			ARN             string `json:"TargetGroupArn"`
			TargetType      string `json:"TargetType"`
			Port            int    `json:"Port"`
			HealthCheckPath string `json:"HealthCheckPath"`
		}{ARN: "arn:second", TargetType: "ip", Port: 5000, HealthCheckPath: "/healthz"},
	)
	arn, path, err = targetGroupForService(document, "proxy", 5000)
	require.ErrorContains(t, err, "multiple IP target groups")
	require.Empty(t, arn)
	require.Empty(t, path)

	document.TargetGroups = document.TargetGroups[:1]
	arn, path, err = targetGroupForService(document, "proxy", 5000)
	require.NoError(t, err)
	require.Equal(t, "arn:first", arn)
	require.Equal(t, "/healthz", path)
}
