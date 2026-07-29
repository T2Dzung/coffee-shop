package prod

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/component"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/config"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/gitops"
)

func (o *RealOperations) Verify(ctx context.Context, action Action) error {
	if action == ActionTeardown {
		return o.verifyTeardown(ctx)
	}
	if action == ActionResilience {
		return o.runResilience(ctx)
	}
	if err := o.initRemote(ctx, o.FoundationTF, o.Config.FoundationStateKey); err != nil {
		return err
	}
	if err := o.loadRuntimeOutputs(ctx); err != nil {
		return err
	}
	if err := o.updateKubeconfig(ctx); err != nil {
		return err
	}
	if action == ActionSetup || action == ActionReconcile {
		exitCode, err := o.FoundationTF.DetailedPlan(ctx)
		if err != nil {
			return err
		}
		if exitCode != 0 {
			return fmt.Errorf("Terraform desired state is not empty after %s", action)
		}
	}
	for _, application := range []string{"coffeeshop-prod-platform", "coffeeshop-prod", "coffeeshop-prod-ownership-guard"} {
		data, err := o.Kube.Kubectl(ctx, nil,
			"get", "application", application, "-n", "argocd", "-o", "json")
		if err != nil {
			return err
		}
		if err := gitops.Evaluate([]byte(data)); err != nil {
			return fmt.Errorf("%s: %w", application, err)
		}
	}
	for _, deployment := range []string{"web", "proxy", "product", "counter", "barista", "kitchen"} {
		if _, err := o.Kube.Kubectl(ctx, nil,
			"rollout", "status", "deployment/"+deployment,
			"-n", "coffeeshop",
			"--timeout="+o.Config.WaitTimeout,
		); err != nil {
			return err
		}
	}
	if _, err := o.Kube.Kubectl(ctx, nil,
		"rollout", "status", "deployment/platform-ownership-guard-controller-manager",
		"-n", "platform-ownership-guard-system", "--timeout="+o.Config.WaitTimeout,
	); err != nil {
		return fmt.Errorf("Guard controller rollout: %w", err)
	}
	if _, err := o.Kube.Kubectl(ctx, nil,
		"get", "lease", "platform-ownership-guard-leader",
		"-n", "platform-ownership-guard-system",
	); err != nil {
		return fmt.Errorf("Guard leader Lease: %w", err)
	}
	if _, err := o.Kube.Kubectl(ctx, nil,
		"wait", "--for=condition=Ready", "ownershipaudit/coffeeshop-ownership",
		"-n", "coffeeshop", "--timeout="+o.Config.WaitTimeout,
	); err != nil {
		return fmt.Errorf("Guard OwnershipAudit readiness: %w", err)
	}
	guardIdentity := "system:serviceaccount:platform-ownership-guard-system:platform-ownership-guard-controller-manager"
	allowed, err := o.Kube.CanI(ctx,
		"patch", "deployments.apps", "coffeeshop", guardIdentity,
	)
	if err != nil {
		return fmt.Errorf("Guard negative RBAC check: %w", err)
	}
	if allowed {
		return fmt.Errorf("Guard service account can mutate target Deployments")
	}
	if err := o.verifyIngress(ctx, transactionProbeRequired(action)); err != nil {
		return err
	}
	if err := o.verifyRuntimeDigest(ctx); err != nil {
		return err
	}
	if action == ActionSetup || action == ActionReconcile {
		return o.verifyArgoSelfHeal(ctx)
	}
	return nil
}

func transactionProbeRequired(action Action) bool {
	return action == ActionSetup || action == ActionReconcile
}

func (o *RealOperations) waitArgo(ctx context.Context, name string) error {
	return o.wait(ctx, "Argo Application "+name, func(ctx context.Context) (bool, error) {
		data, err := o.Kube.Kubectl(ctx, nil,
			"get", "application", name, "-n", "argocd", "-o", "json")
		if err != nil {
			return false, nil
		}
		return gitops.Evaluate([]byte(data)) == nil, nil
	})
}

func (o *RealOperations) verifyRuntimeDigest(ctx context.Context) error {
	image, err := o.Kube.Kubectl(ctx, nil,
		"get", "deployment", "web", "-n", "coffeeshop",
		"-o", `jsonpath={.spec.template.spec.containers[?(@.name=="web")].image}`,
	)
	if err != nil {
		return err
	}
	parts := strings.Split(image, "@")
	if len(parts) != 2 || !digestPattern.MatchString(parts[1]) {
		return fmt.Errorf("web Deployment is not pinned to an immutable digest")
	}
	if err := o.AWS.Run(ctx,
		"ecr", "describe-images",
		"--repository-name", "go-coffeeshop-web",
		"--image-ids", "imageDigest="+parts[1],
	); err != nil {
		return fmt.Errorf("web digest is absent from PROD ECR: %w", err)
	}
	pods, err := o.pods(ctx, "web")
	if err != nil {
		return err
	}
	running, err := runningWebDigest(pods)
	if err != nil {
		return err
	}
	if running != parts[1] {
		return fmt.Errorf("running web digest %s does not match Deployment digest %s", running, parts[1])
	}
	return nil
}

func (o *RealOperations) verifyTeardown(ctx context.Context) error {
	if o.cluster != "" && o.awsSucceeds(ctx, "eks", "describe-cluster", "--name", o.cluster) {
		return fmt.Errorf("billable orphan: EKS cluster %s remains", o.cluster)
	}
	if o.vpcID != "" && o.awsSucceeds(ctx, "ec2", "describe-vpcs", "--vpc-ids", o.vpcID) {
		return fmt.Errorf("billable orphan: VPC %s remains", o.vpcID)
	}
	checks := []struct {
		description string
		args        []string
	}{
		{"EC2 instances", []string{"ec2", "describe-instances", "--filters",
			"Name=tag:Project,Values=" + o.Config.ProjectName,
			"Name=tag:Environment,Values=" + o.Config.Environment,
			"Name=instance-state-name,Values=pending,running,shutting-down,stopping,stopped",
			"--query", "length(Reservations[].Instances[])", "--output", "text"}},
		{"EBS volumes", []string{"ec2", "describe-volumes", "--filters",
			"Name=tag:Project,Values=" + o.Config.ProjectName,
			"Name=tag:Environment,Values=" + o.Config.Environment,
			"--query", "length(Volumes)", "--output", "text"}},
		{"NAT gateways", []string{"ec2", "describe-nat-gateways", "--filter",
			"Name=tag:Project,Values=" + o.Config.ProjectName,
			"Name=tag:Environment,Values=" + o.Config.Environment,
			"--query", "length(NatGateways[?State!=`deleted`])", "--output", "text"}},
		{"Elastic IPs", []string{"ec2", "describe-addresses", "--filters",
			"Name=tag:Project,Values=" + o.Config.ProjectName,
			"Name=tag:Environment,Values=" + o.Config.Environment,
			"--query", "length(Addresses)", "--output", "text"}},
		{"tagged load balancers", taggedResourceCountArgs(o.Config, "elasticloadbalancing:loadbalancer")},
		{"tagged target groups", taggedResourceCountArgs(o.Config, "elasticloadbalancing:targetgroup")},
		{"tagged security groups", taggedResourceCountArgs(o.Config, "ec2:security-group")},
		{"EKS and Container Insights log groups", []string{"logs", "describe-log-groups",
			"--query", "length(logGroups[?starts_with(logGroupName, '/aws/eks/" + o.cluster +
				"/') || starts_with(logGroupName, '/aws/containerinsights/" + o.cluster + "/')])",
			"--output", "text"}},
		{"RDS instances", []string{"rds", "describe-db-instances",
			"--query", "length(DBInstances[?DBInstanceIdentifier=='" + o.Config.ProjectName + "-" +
				o.Config.Environment + "-db'])", "--output", "text"}},
		{"application Secrets Manager secrets", []string{"secretsmanager", "list-secrets",
			"--include-planned-deletion",
			"--query", "length(SecretList[?Name=='/" + o.Config.ProjectName + "/" +
				o.Config.Environment + "/application'])", "--output", "text"}},
	}
	for _, check := range checks {
		value, err := o.AWS.Text(ctx, check.args...)
		if err != nil {
			return err
		}
		count, err := strconv.Atoi(value)
		if err != nil || count != 0 {
			return fmt.Errorf("billable orphan inventory: %s=%s", check.description, value)
		}
	}
	if !o.awsSucceeds(ctx, "s3api", "head-bucket", "--bucket", o.Config.StateBucket) {
		return fmt.Errorf("retained backend bucket is missing")
	}
	if !o.awsSucceeds(ctx, "kms", "describe-key", "--key-id", "alias/"+o.Config.ProjectName+"-state-key") {
		return fmt.Errorf("retained backend KMS key is missing")
	}
	catalog, err := component.Load(filepath.Join(o.Config.ProjectRoot, "platform", "components.yaml"))
	if err != nil {
		return err
	}
	for _, item := range catalog.Components {
		if !o.awsSucceeds(ctx, "ecr", "describe-repositories", "--repository-names", item.ImageRepository) {
			return fmt.Errorf("retained ECR repository %s is missing", item.ImageRepository)
		}
		if !o.awsSucceeds(ctx, "ecr", "get-lifecycle-policy", "--repository-name", item.ImageRepository) {
			return fmt.Errorf("retained ECR lifecycle policy for %s is missing", item.ImageRepository)
		}
		candidate := component.CandidateRepositoryName(item)
		if !o.awsSucceeds(ctx, "ecr", "describe-repositories", "--repository-names", candidate) {
			return fmt.Errorf("retained candidate ECR repository %s is missing", candidate)
		}
		if !o.awsSucceeds(ctx, "ecr", "get-lifecycle-policy", "--repository-name", candidate) {
			return fmt.Errorf("retained candidate ECR lifecycle policy for %s is missing", candidate)
		}
	}
	for _, role := range []string{
		o.Config.ProjectName + "-prod-github-delivery-role",
		o.Config.ProjectName + "-prod-github-emergency-delivery-role",
		o.Config.ProjectName + "-dev-candidate-reader-role",
	} {
		if !o.awsSucceeds(ctx, "iam", "get-role", "--role-name", role) {
			return fmt.Errorf("retained GitHub delivery role %s is missing", role)
		}
	}
	if !o.awsSucceeds(ctx,
		"budgets", "describe-budget",
		"--account-id", o.Config.AccountID,
		"--budget-name", o.Config.ProjectName+"-"+o.Config.Environment+"-monthly-budget",
	) {
		return fmt.Errorf("retained AWS Budget is missing")
	}
	fmt.Fprintf(o.Output,
		"Teardown inventory passed; retained backend S3/KMS, %d PROD + %d candidate ECR repositories, GitHub delivery roles and AWS Budget confirmed.\n",
		len(catalog.Components), len(catalog.Components),
	)
	return nil
}

func taggedResourceCountArgs(cfg config.Prod, resourceType string) []string {
	return []string{
		"resourcegroupstaggingapi", "get-resources",
		"--resource-type-filters", resourceType,
		"--tag-filters",
		"Key=Project,Values=" + cfg.ProjectName,
		"Key=Environment,Values=" + cfg.Environment,
		"--query", "length(ResourceTagMappingList)",
		"--output", "text",
	}
}
