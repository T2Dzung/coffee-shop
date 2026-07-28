package prod

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
