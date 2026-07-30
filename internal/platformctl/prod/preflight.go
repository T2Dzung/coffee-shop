package prod

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	platformaws "github.com/thangchung/go-coffeeshop/internal/platformctl/aws"
)

func (o *RealOperations) Preflight(ctx context.Context, action Action) error {
	for _, name := range []string{"aws", "terraform", "kubectl", "helm", "git", "curl", "conftest"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("missing required command %s", name)
		}
	}
	if err := os.MkdirAll(filepath.Dir(o.Config.Kubeconfig), 0o700); err != nil {
		return err
	}
	var caller struct {
		Account string `json:"Account"`
		ARN     string `json:"Arn"`
	}
	if err := o.AWS.JSON(ctx, &caller, "sts", "get-caller-identity", "--output", "json"); err != nil {
		return err
	}
	if caller.Account != o.Config.AccountID {
		return fmt.Errorf("caller account %s does not match target %s", caller.Account, o.Config.AccountID)
	}
	if err := o.Policy.Config(ctx, map[string]string{
		"environment":          o.Config.Environment,
		"bootstrap_state_key":  o.Config.BootstrapStateKey,
		"foundation_state_key": o.Config.FoundationStateKey,
	}); err != nil {
		return err
	}
	fmt.Fprintf(o.Output, "Caller ARN: %s\nTarget account: %s\nTarget Region: %s\n",
		caller.ARN, caller.Account, o.Config.Region)
	if action == ActionSetup || action == ActionReconcile || action == ActionTeardown {
		if err := o.showHourlyEstimate(ctx); err != nil {
			return err
		}
	}
	if action == ActionSetup || action == ActionReconcile {
		if err := o.verifyManagedServiceAvailability(ctx); err != nil {
			return err
		}
	}
	switch action {
	case ActionTeardown:
		if os.Getenv("PROD_CONFIRM_TEARDOWN") != o.Config.AccountID {
			return fmt.Errorf("set PROD_CONFIRM_TEARDOWN=%s for account-scoped teardown", o.Config.AccountID)
		}
		restoreTargets, err := o.AWS.FindTaggedRDSResources(ctx, map[string]string{
			"Project": o.Config.ProjectName, "Environment": o.Config.Environment, "Purpose": "restore-drill",
		})
		if err != nil {
			return fmt.Errorf("restore drill inventory before teardown: %w", err)
		}
		if len(restoreTargets) != 0 {
			return fmt.Errorf("cleanup restore drill target before PROD teardown: %s", restoreTargets[0].ARN)
		}
	case ActionResilience:
		if os.Getenv("PROD_CONFIRM_RESILIENCE") != o.Config.AccountID {
			return fmt.Errorf("set PROD_CONFIRM_RESILIENCE=%s for controlled failure fixtures", o.Config.AccountID)
		}
	}
	return nil
}

func (o *RealOperations) showHourlyEstimate(ctx context.Context) error {
	estimate, err := (platformaws.Pricing{Client: platformaws.Client{
		Runner: o.Runner, Region: "us-east-1", Profile: o.Config.AWSProfile, Timeout: 2 * time.Minute,
	}}).Estimate(ctx, platformaws.EstimateInput{
		Region:           o.Config.Region,
		NodeInstanceType: o.Config.NodeInstanceTypes[0],
		NodeCount:        o.Config.NodeDesiredSize,
		NodeDiskGiB:      o.Config.NodeDiskGiB,
		ALBCount:         1,
		PublicIPv4Count:  3,
		RDSInstanceClass: o.Config.RDSInstanceClass,
		RDSStorageGiB:    o.Config.RDSStorageGiB,
		RabbitMQEBSGiB:   15,
	})
	if err != nil {
		return fmt.Errorf("dynamic hourly estimate: %w", err)
	}
	fmt.Fprintf(o.Output, `Estimated fixed cost for 1 hour (%s, current AWS On-Demand rates):
  EKS control plane       : USD %.4f
  %d x %s nodes : USD %.4f
  NAT Gateway             : USD %.4f
  Node + RabbitMQ gp3     : USD %.4f
  RDS %s       : USD %.4f
  RDS gp3 storage         : USD %.4f
  Application LB          : USD %.4f
  Public IPv4             : USD %.4f
  --------------------------------------
  Fixed hourly estimate   : USD %.4f
Usage-priced traffic, LCU, logs, requests and taxes are excluded.
`,
		o.Config.Region, estimate.EKS,
		o.Config.NodeDesiredSize, o.Config.NodeInstanceTypes[0], estimate.Nodes,
		estimate.NAT, estimate.EBS, o.Config.RDSInstanceClass, estimate.RDS,
		estimate.RDSStorage, estimate.ALB, estimate.PublicIPv4, estimate.Total(),
	)
	return nil
}

func (o *RealOperations) verifyManagedServiceAvailability(ctx context.Context) error {
	count, err := o.AWS.Text(ctx,
		"rds", "describe-orderable-db-instance-options",
		"--engine", "postgres",
		"--engine-version", o.Config.RDSEngineVersion,
		"--db-instance-class", o.Config.RDSInstanceClass,
		"--query", "length(OrderableDBInstanceOptions)",
		"--output", "text",
	)
	if err != nil {
		return fmt.Errorf("query RDS orderability: %w", err)
	}
	available, err := strconv.Atoi(count)
	if err != nil || available < 1 {
		return fmt.Errorf("PostgreSQL %s/%s is not orderable in %s",
			o.Config.RDSEngineVersion, o.Config.RDSInstanceClass, o.Config.Region)
	}
	for _, addon := range []struct {
		name, version string
	}{
		{"aws-ebs-csi-driver", o.Config.EBSAddonVersion},
		{"amazon-cloudwatch-observability", o.Config.CloudWatchVersion},
	} {
		version, err := o.AWS.Text(ctx,
			"eks", "describe-addon-versions",
			"--addon-name", addon.name,
			"--kubernetes-version", o.Config.ClusterVersion,
			"--query", "addons[0].addonVersions[?addonVersion=='"+addon.version+"'].addonVersion | [0]",
			"--output", "text",
		)
		if err != nil {
			return fmt.Errorf("query EKS add-on %s: %w", addon.name, err)
		}
		if version != addon.version {
			return fmt.Errorf("%s %s is not compatible with EKS %s",
				addon.name, addon.version, o.Config.ClusterVersion)
		}
	}
	if o.Config.SLOEnabled {
		runtime, err := o.AWS.Text(ctx,
			"synthetics", "describe-runtime-versions",
			"--query", "RuntimeVersions[?VersionName=='"+o.Config.SyntheticsRuntime+"'].VersionName | [0]",
			"--output", "text",
		)
		if err != nil {
			return fmt.Errorf("query CloudWatch Synthetics runtime: %w", err)
		}
		if runtime != o.Config.SyntheticsRuntime {
			return fmt.Errorf("CloudWatch Synthetics runtime %s is unavailable in %s",
				o.Config.SyntheticsRuntime, o.Config.Region)
		}
		fmt.Fprintf(o.Output, "Synthetics runtime preflight passed: %s.\n", o.Config.SyntheticsRuntime)
	}
	fmt.Fprintf(o.Output, "Managed service preflight passed: PostgreSQL %s, EBS CSI %s, CloudWatch %s.\n",
		o.Config.RDSEngineVersion, o.Config.EBSAddonVersion, o.Config.CloudWatchVersion)
	return nil
}
