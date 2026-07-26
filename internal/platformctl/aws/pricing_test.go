package aws

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodePriceSelectsUnit(t *testing.T) {
	t.Parallel()
	value, err := decodePrice([]byte(`{"PriceList":["{\"terms\":{\"OnDemand\":{\"x\":{\"priceDimensions\":{\"a\":{\"unit\":\"GB-Mo\",\"pricePerUnit\":{\"USD\":\"0.08\"}},\"b\":{\"unit\":\"Hrs\",\"pricePerUnit\":{\"USD\":\"0.12\"}}}}}}}"]}`), "Hrs")
	require.NoError(t, err)
	require.Equal(t, 0.12, value)
}

func TestHourlyEstimateTotal(t *testing.T) {
	t.Parallel()
	require.Equal(t, 36.0, (HourlyEstimate{
		EKS: 1, Nodes: 2, NAT: 3, EBS: 4, RDS: 5, RDSStorage: 6, ALB: 7, PublicIPv4: 8,
	}).Total())
}
