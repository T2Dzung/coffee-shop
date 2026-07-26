package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

type PriceFilter struct {
	Field string
	Value string
}

type Pricing struct {
	Client Client
}

type EstimateInput struct {
	Region           string
	NodeInstanceType string
	NodeCount        int
	NodeDiskGiB      int
	ALBCount         int
	PublicIPv4Count  int
	RDSInstanceClass string
	RDSStorageGiB    int
	RabbitMQEBSGiB   int
}

type HourlyEstimate struct {
	EKS        float64
	Nodes      float64
	NAT        float64
	EBS        float64
	RDS        float64
	RDSStorage float64
	ALB        float64
	PublicIPv4 float64
}

type CIEstimateInput struct {
	Region       string
	InstanceType string
	DiskGiB      int
}

type CIHourlyEstimate struct {
	Instance   float64
	EBS        float64
	PublicIPv4 float64
}

func (e CIHourlyEstimate) Total() float64 { return e.Instance + e.EBS + e.PublicIPv4 }

func (p Pricing) EstimateCI(ctx context.Context, input CIEstimateInput) (CIHourlyEstimate, error) {
	region := PriceFilter{Field: "regionCode", Value: input.Region}
	instance, err := p.price(ctx, "AmazonEC2", "", region,
		PriceFilter{Field: "instanceType", Value: input.InstanceType},
		PriceFilter{Field: "operatingSystem", Value: "Linux"},
		PriceFilter{Field: "tenancy", Value: "Shared"},
		PriceFilter{Field: "preInstalledSw", Value: "NA"},
		PriceFilter{Field: "capacitystatus", Value: "Used"})
	if err != nil {
		return CIHourlyEstimate{}, err
	}
	gp3, err := p.price(ctx, "AmazonEC2", "", region,
		PriceFilter{Field: "productFamily", Value: "Storage"},
		PriceFilter{Field: "volumeApiName", Value: "gp3"})
	if err != nil {
		return CIHourlyEstimate{}, err
	}
	ipv4, err := p.price(ctx, "AmazonVPC", "", region,
		PriceFilter{Field: "group", Value: "VPCPublicIPv4Address"},
		PriceFilter{Field: "groupDescription", Value: "Hourly charge for In-use Public IPv4 Addresses"})
	if err != nil {
		return CIHourlyEstimate{}, err
	}
	const hoursPerMonth = 730.0
	return CIHourlyEstimate{
		Instance: instance, EBS: gp3 * float64(input.DiskGiB) / hoursPerMonth, PublicIPv4: ipv4,
	}, nil
}

func (e HourlyEstimate) Total() float64 {
	return e.EKS + e.Nodes + e.NAT + e.EBS + e.RDS + e.RDSStorage + e.ALB + e.PublicIPv4
}

func (p Pricing) Estimate(ctx context.Context, input EstimateInput) (HourlyEstimate, error) {
	region := PriceFilter{Field: "regionCode", Value: input.Region}
	eks, err := p.price(ctx, "AmazonEKS", "", region,
		PriceFilter{Field: "locationType", Value: "AWS Region"},
		PriceFilter{Field: "tiertype", Value: "HAStandard"})
	if err != nil {
		return HourlyEstimate{}, err
	}
	instance, err := p.price(ctx, "AmazonEC2", "", region,
		PriceFilter{Field: "instanceType", Value: input.NodeInstanceType},
		PriceFilter{Field: "operatingSystem", Value: "Linux"},
		PriceFilter{Field: "tenancy", Value: "Shared"},
		PriceFilter{Field: "preInstalledSw", Value: "NA"},
		PriceFilter{Field: "capacitystatus", Value: "Used"})
	if err != nil {
		return HourlyEstimate{}, err
	}
	nat, err := p.price(ctx, "AmazonEC2", "", region,
		PriceFilter{Field: "productFamily", Value: "NAT Gateway"})
	if err != nil {
		return HourlyEstimate{}, err
	}
	gp3, err := p.price(ctx, "AmazonEC2", "", region,
		PriceFilter{Field: "productFamily", Value: "Storage"},
		PriceFilter{Field: "volumeApiName", Value: "gp3"})
	if err != nil {
		return HourlyEstimate{}, err
	}
	ipv4, err := p.price(ctx, "AmazonVPC", "", region,
		PriceFilter{Field: "group", Value: "VPCPublicIPv4Address"},
		PriceFilter{Field: "groupDescription", Value: "Hourly charge for In-use Public IPv4 Addresses"})
	if err != nil {
		return HourlyEstimate{}, err
	}
	rds, err := p.price(ctx, "AmazonRDS", "Hrs", region,
		PriceFilter{Field: "productFamily", Value: "Database Instance"},
		PriceFilter{Field: "instanceType", Value: input.RDSInstanceClass},
		PriceFilter{Field: "databaseEngine", Value: "PostgreSQL"},
		PriceFilter{Field: "deploymentOption", Value: "Single-AZ"})
	if err != nil {
		return HourlyEstimate{}, err
	}
	rdsStorage, err := p.price(ctx, "AmazonRDS", "GB-Mo", region,
		PriceFilter{Field: "productFamily", Value: "Database Storage"},
		PriceFilter{Field: "databaseEngine", Value: "PostgreSQL"},
		PriceFilter{Field: "deploymentOption", Value: "Single-AZ"},
		PriceFilter{Field: "volumeType", Value: "General Purpose-GP3"})
	if err != nil {
		return HourlyEstimate{}, err
	}
	alb := 0.0
	if input.ALBCount > 0 {
		alb, err = p.price(ctx, "AWSELB", "Hrs", region,
			PriceFilter{Field: "productFamily", Value: "Load Balancer-Application"},
			PriceFilter{Field: "operation", Value: "LoadBalancing:Application"})
		if err != nil {
			return HourlyEstimate{}, err
		}
	}
	const hoursPerMonth = 730.0
	return HourlyEstimate{
		EKS:        eks,
		Nodes:      instance * float64(input.NodeCount),
		NAT:        nat,
		EBS:        gp3 * float64(input.NodeDiskGiB*input.NodeCount+input.RabbitMQEBSGiB) / hoursPerMonth,
		RDS:        rds,
		RDSStorage: rdsStorage * float64(input.RDSStorageGiB) / hoursPerMonth,
		ALB:        alb * float64(input.ALBCount),
		PublicIPv4: ipv4 * float64(input.PublicIPv4Count),
	}, nil
}

func (p Pricing) price(ctx context.Context, service, unit string, filters ...PriceFilter) (float64, error) {
	args := []string{"pricing", "get-products", "--region", "us-east-1", "--service-code", service, "--filters"}
	for _, filter := range filters {
		args = append(args, "Type=TERM_MATCH,Field="+filter.Field+",Value="+filter.Value)
	}
	args = append(args, "--max-results", "20", "--output", "json")
	result, err := p.Client.run(ctx, args...)
	if err != nil {
		return 0, err
	}
	value, err := decodePrice([]byte(result.Stdout), unit)
	if err != nil {
		return 0, fmt.Errorf("%s price: %w", service, err)
	}
	return value, nil
}

func decodePrice(data []byte, requiredUnit string) (float64, error) {
	var response struct {
		PriceList []string `json:"PriceList"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return 0, err
	}
	for _, raw := range response.PriceList {
		var product struct {
			Terms struct {
				OnDemand map[string]struct {
					Dimensions map[string]struct {
						Unit  string            `json:"unit"`
						Price map[string]string `json:"pricePerUnit"`
					} `json:"priceDimensions"`
				} `json:"OnDemand"`
			} `json:"terms"`
		}
		if err := json.Unmarshal([]byte(raw), &product); err != nil {
			continue
		}
		for _, term := range product.Terms.OnDemand {
			for _, dimension := range term.Dimensions {
				if requiredUnit != "" && dimension.Unit != requiredUnit {
					continue
				}
				if value := dimension.Price["USD"]; value != "" {
					return strconv.ParseFloat(value, 64)
				}
			}
		}
	}
	return 0, fmt.Errorf("no current On-Demand price found")
}
