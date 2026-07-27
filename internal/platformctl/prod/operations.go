package prod

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	platformaws "github.com/thangchung/go-coffeeshop/internal/platformctl/aws"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/config"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/kubernetes"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/policy"
	platformterraform "github.com/thangchung/go-coffeeshop/internal/platformctl/terraform"
)

const (
	loadBalancerControllerChart = "3.4.2"
	argoCDChart                 = "6.7.18"
	externalSecretsChart        = "2.5.0"
	certManagerChart            = "v1.20.0"
)

var digestPattern = regexp.MustCompile(`sha256:[0-9a-f]{64}`)

type RealOperations struct {
	Config       config.Prod
	Runner       command.Runner
	Approver     Approver
	Output       io.Writer
	AWS          platformaws.Client
	Kube         kubernetes.Client
	Policy       policy.Evaluator
	BootstrapTF  platformterraform.Client
	FoundationTF platformterraform.Client

	albARN  string
	cluster string
	vpcID   string
}

func NewRealOperations(cfg config.Prod, runner command.Runner, approver Approver, output io.Writer) *RealOperations {
	if output == nil {
		output = io.Discard
	}
	cacheRoot := filepath.Join(filepath.Dir(cfg.Kubeconfig), "..", ".cache", "go-coffeeshop", "terraform")
	if home, err := os.UserHomeDir(); err == nil {
		cacheRoot = filepath.Join(home, ".cache", "go-coffeeshop", "terraform")
	}
	timeout := 45 * time.Minute
	return &RealOperations{
		Config: cfg, Runner: runner, Approver: approver, Output: output,
		AWS:    platformaws.Client{Runner: runner, Region: cfg.Region, Profile: cfg.AWSProfile, Timeout: timeout},
		Kube:   kubernetes.Client{Runner: runner, Kubeconfig: cfg.Kubeconfig, Timeout: timeout},
		Policy: policy.Evaluator{Runner: runner, ProjectRoot: cfg.ProjectRoot},
		BootstrapTF: platformterraform.Client{
			Runner:  runner,
			Dir:     filepath.Join(cfg.ProjectRoot, "infrastructure", "terraform", "bootstrap", "prod"),
			DataDir: filepath.Join(cacheRoot, "prod-bootstrap-"+cfg.AccountID),
			Variables: map[string]string{
				"aws_region": cfg.Region, "expected_aws_account_id": cfg.AccountID,
				"project_name": cfg.ProjectName,
			},
			Timeout:     timeout,
			Environment: awsEnvironment(cfg.AWSProfile),
		},
		FoundationTF: platformterraform.Client{
			Runner:      runner,
			Dir:         filepath.Join(cfg.ProjectRoot, "infrastructure", "terraform", "envs", "prod"),
			DataDir:     filepath.Join(cacheRoot, "prod-foundation-"+cfg.AccountID),
			VarFile:     cfg.VarFile,
			Timeout:     timeout,
			Environment: awsEnvironment(cfg.AWSProfile),
		},
	}
}

func awsEnvironment(profile string) map[string]string {
	if profile == "" {
		return nil
	}
	return map[string]string{"AWS_PROFILE": profile}
}

func (o *RealOperations) Plan(ctx context.Context, action Action) (Plan, error) {
	if err := o.initRemote(ctx, o.FoundationTF, o.Config.FoundationStateKey); err != nil {
		return Plan{}, err
	}
	destroy := action == ActionTeardown
	targets := []string(nil)
	policyName := "reconcile"
	if destroy {
		targets = teardownTargets
		policyName = "teardown"
	}
	artifact, err := o.FoundationTF.CreatePlan(ctx, "", "prod-"+string(action), destroy, targets)
	if err != nil {
		return Plan{}, err
	}
	if err := o.Policy.Terraform(ctx, policyName, artifact.JSONPath); err != nil {
		artifact.Cleanup()
		return Plan{}, err
	}
	human, err := o.FoundationTF.ShowHuman(ctx, artifact)
	if err != nil {
		artifact.Cleanup()
		return Plan{}, err
	}
	plan := Plan{Artifact: artifact, Human: human}
	return plan, nil
}

func (o *RealOperations) BeforeApply(ctx context.Context, action Action) error {
	if action != ActionTeardown {
		return nil
	}
	if err := o.loadRuntimeOutputs(ctx); err != nil {
		return err
	}
	if err := o.updateKubeconfig(ctx); err != nil {
		return err
	}
	hostname, _ := o.Kube.Kubectl(ctx, nil, "get", "ingress", "coffeeshop-prod-alb-ingress",
		"-n", "coffeeshop", "-o", "jsonpath={.status.loadBalancer.ingress[0].hostname}")
	if hostname != "" {
		o.albARN, _ = o.AWS.Text(ctx, "elbv2", "describe-load-balancers",
			"--query", "LoadBalancers[?DNSName=='"+hostname+"'].LoadBalancerArn | [0]", "--output", "text")
	}
	if o.kubeSucceeds(ctx, "get", "application", "coffeeshop-prod-ownership-guard", "-n", "argocd") {
		if _, err := o.Kube.Kubectl(ctx, nil, "delete", "application", "coffeeshop-prod-ownership-guard",
			"-n", "argocd", "--wait=true", "--timeout="+o.Config.WaitTimeout); err != nil {
			return err
		}
	}

	if o.kubeSucceeds(ctx, "get", "application", "coffeeshop-prod", "-n", "argocd") {
		if _, err := o.Kube.Kubectl(ctx, nil, "delete", "application", "coffeeshop-prod", "-n", "argocd",
			"--wait=true", "--timeout="+o.Config.WaitTimeout); err != nil {
			return err
		}
	} else if o.kubeSucceeds(ctx, "get", "ingress", "coffeeshop-prod-alb-ingress", "-n", "coffeeshop") {
		if _, err := o.Kube.Kubectl(ctx, nil, "delete", "ingress", "coffeeshop-prod-alb-ingress",
			"-n", "coffeeshop", "--wait=true", "--timeout="+o.Config.WaitTimeout); err != nil {
			return err
		}
	}
	if o.kubeSucceeds(ctx, "get", "application", "coffeeshop-prod-platform", "-n", "argocd") {
		if _, err := o.Kube.Kubectl(ctx, nil, "delete", "application", "coffeeshop-prod-platform",
			"-n", "argocd", "--wait=true", "--timeout="+o.Config.WaitTimeout); err != nil {
			return err
		}
	}
	if o.albARN != "" && o.albARN != "None" {
		if err := o.wait(ctx, "ALB deletion", func(ctx context.Context) (bool, error) {
			_, err := o.AWS.Text(ctx, "elbv2", "describe-load-balancers", "--load-balancer-arns", o.albARN)
			return err != nil, nil
		}); err != nil {
			return err
		}
	}
	for _, release := range []struct{ name, namespace string }{
		{"argocd", "argocd"},
		{"aws-load-balancer-controller", "kube-system"},
		{"external-secrets", "external-secrets"},
		{"cert-manager", "cert-manager"},
	} {
		if o.helmSucceeds(ctx, "status", release.name, "-n", release.namespace) {
			if _, err := o.Kube.Helm(ctx, "uninstall", release.name, "-n", release.namespace,
				"--wait", "--timeout", o.Config.WaitTimeout); err != nil {
				return err
			}
		}
	}
	_, err := o.Kube.Kubectl(ctx, nil, "delete", "namespace", "rabbitmq-system",
		"--ignore-not-found", "--wait=true", "--timeout="+o.Config.WaitTimeout)
	return err
}

func (o *RealOperations) Apply(ctx context.Context, plan Plan) error {
	return o.FoundationTF.Apply(ctx, plan.Artifact)
}

func (o *RealOperations) Configure(ctx context.Context) error {
	if err := o.loadRuntimeOutputs(ctx); err != nil {
		return err
	}
	if err := o.printGitHubSettings(ctx); err != nil {
		return err
	}
	if err := o.updateKubeconfig(ctx); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"wait", "--for=condition=Ready", "nodes", "--all", "--timeout=" + o.Config.WaitTimeout},
		{"rollout", "status", "daemonset/aws-node", "-n", "kube-system", "--timeout=" + o.Config.WaitTimeout},
		{"rollout", "status", "deployment/coredns", "-n", "kube-system", "--timeout=" + o.Config.WaitTimeout},
	} {
		if _, err := o.Kube.Kubectl(ctx, nil, args...); err != nil {
			return err
		}
	}
	if err := o.seedApplicationSecret(ctx); err != nil {
		return err
	}
	if err := o.installPlatformControllers(ctx); err != nil {
		return err
	}
	if err := o.waitForPromotedDigests(ctx); err != nil {
		return err
	}
	if err := o.applyBootstrapManifest(ctx, "appproject.yaml", nil); err != nil {
		return err
	}
	if err := o.applyBootstrapManifest(ctx, "coffeeshop-prod-platform-app.yaml", nil); err != nil {
		return err
	}
	if err := o.wait(ctx, "ClusterSecretStore", func(ctx context.Context) (bool, error) {
		return o.kubeSucceeds(ctx, "get", "clustersecretstore", "aws-secretsmanager"), nil
	}); err != nil {
		return err
	}
	if err := o.applyRDSBootstrapSecret(ctx); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"wait", "--for=condition=Ready", "clustersecretstore/aws-secretsmanager", "--timeout=" + o.Config.WaitTimeout},
		{"wait", "--for=condition=Ready", "externalsecret/coffeeshop-secret", "-n", "coffeeshop", "--timeout=" + o.Config.WaitTimeout},
		{"wait", "--for=condition=Ready", "externalsecret/coffeeshop-rabbitmq-default-user", "-n", "coffeeshop", "--timeout=" + o.Config.WaitTimeout},
		{"wait", "--for=condition=Ready", "externalsecret/coffeeshop-rds-master-bootstrap", "-n", "coffeeshop", "--timeout=" + o.Config.WaitTimeout},
	} {
		if _, err := o.Kube.Kubectl(ctx, nil, args...); err != nil {
			return err
		}
	}
	if err := o.waitArgo(ctx, "coffeeshop-prod-platform"); err != nil {
		return err
	}
	if _, err := o.Kube.Kubectl(ctx, nil, "wait", "--for=condition=AllReplicasReady",
		"rabbitmqcluster/coffeeshop-rabbitmq", "-n", "coffeeshop", "--timeout="+o.Config.WaitTimeout); err != nil {
		return err
	}
	if err := o.applyBootstrapManifest(ctx, "coffeeshop-prod-app.yaml", nil); err != nil {
		return err
	}
	if err := o.waitArgo(ctx, "coffeeshop-prod"); err != nil {
		return err
	}
	if err := o.applyBootstrapManifest(ctx, "coffeeshop-prod-ownership-guard-app.yaml", nil); err != nil {
		return err
	}
	return o.waitArgo(ctx, "coffeeshop-prod-ownership-guard")
}

func (o *RealOperations) printGitHubSettings(ctx context.Context) error {
	outputs := []struct {
		name  string
		label string
	}{
		{"github_delivery_role_arn", "PROD_AWS_ROLE_ARN"},
		{"github_emergency_delivery_role_arn", "PROD_EMERGENCY_AWS_ROLE_ARN"},
		{"github_dev_candidate_reader_role_arn", "DEV_CANDIDATE_READER_ROLE_ARN"},
	}
	fmt.Fprintln(o.Output, "GitHub Environment values resolved from Terraform:")
	for _, item := range outputs {
		value, err := o.FoundationTF.Output(ctx, item.name)
		if err != nil {
			return fmt.Errorf("read %s: %w", item.name, err)
		}
		fmt.Fprintf(o.Output, "  %s=%s\n", item.label, strings.TrimSpace(value))
	}
	fmt.Fprintf(o.Output, "  PROD_AWS_REGION=%s\n  CANDIDATE_AWS_REGION=%s\n", o.Config.Region, o.Config.Region)
	return nil
}

func (o *RealOperations) loadRuntimeOutputs(ctx context.Context) error {
	var err error
	o.cluster, err = o.FoundationTF.Output(ctx, "cluster_name")
	if err != nil {
		return err
	}
	o.cluster = strings.TrimSpace(o.cluster)
	o.vpcID, err = o.FoundationTF.Output(ctx, "vpc_id")
	o.vpcID = strings.TrimSpace(o.vpcID)
	return err
}

func (o *RealOperations) updateKubeconfig(ctx context.Context) error {
	arn, err := o.FoundationTF.Output(ctx, "cluster_arn")
	if err != nil {
		return err
	}
	return o.AWS.Run(ctx, "eks", "update-kubeconfig", "--name", o.cluster, "--region", o.Config.Region,
		"--kubeconfig", o.Config.Kubeconfig, "--alias", strings.TrimSpace(arn))
}

func (o *RealOperations) installPlatformControllers(ctx context.Context) error {
	commands := [][]string{
		{"repo", "add", "eks", "https://aws.github.io/eks-charts", "--force-update"},
		{"upgrade", "--install", "aws-load-balancer-controller", "eks/aws-load-balancer-controller",
			"--namespace", "kube-system", "--version", loadBalancerControllerChart,
			"--values", filepath.Join(o.Config.ProjectRoot, "infrastructure/k8s/environments/prod/platform/aws-load-balancer-controller-values.yaml"),
			"--set-string", "clusterName=" + o.cluster, "--set-string", "region=" + o.Config.Region,
			"--set-string", "vpcId=" + o.vpcID, "--atomic", "--wait", "--timeout", o.Config.WaitTimeout},
		{"repo", "add", "jetstack", "https://charts.jetstack.io", "--force-update"},
		{"upgrade", "--install", "cert-manager", "jetstack/cert-manager", "--namespace", "cert-manager",
			"--create-namespace", "--version", certManagerChart, "--set", "installCRDs=true",
			"--atomic", "--wait", "--timeout", o.Config.WaitTimeout},
		{"repo", "add", "external-secrets", "https://charts.external-secrets.io", "--force-update"},
		{"upgrade", "--install", "external-secrets", "external-secrets/external-secrets",
			"--namespace", "external-secrets", "--create-namespace", "--version", externalSecretsChart,
			"--set", "installCRDs=true", "--set", "serviceAccount.name=external-secrets-sa",
			"--atomic", "--wait", "--timeout", o.Config.WaitTimeout},
		{"repo", "add", "argo", "https://argoproj.github.io/argo-helm", "--force-update"},
		{"upgrade", "--install", "argocd", "argo/argo-cd", "--namespace", "argocd", "--create-namespace",
			"--version", argoCDChart,
			"--values", filepath.Join(o.Config.ProjectRoot, "infrastructure/k8s/environments/prod/platform/argocd-values.yaml"),
			"--atomic", "--wait", "--timeout", o.Config.WaitTimeout},
	}
	for _, args := range commands {
		if _, err := o.Kube.Helm(ctx, args...); err != nil {
			return err
		}
	}
	rabbit := filepath.Join(o.Config.ProjectRoot,
		"infrastructure/k8s/environments/dev/gitops/addons/rabbitmq-operator/cluster-operator.yaml")
	if _, err := o.Kube.Kubectl(ctx, nil, "apply", "--server-side", "--force-conflicts", "-f", rabbit); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"rollout", "status", "deployment/aws-load-balancer-controller", "-n", "kube-system", "--timeout=" + o.Config.WaitTimeout},
		{"rollout", "status", "deployment/external-secrets", "-n", "external-secrets", "--timeout=" + o.Config.WaitTimeout},
		{"rollout", "status", "deployment/rabbitmq-cluster-operator", "-n", "rabbitmq-system", "--timeout=" + o.Config.WaitTimeout},
		{"rollout", "status", "deployment/argocd-server", "-n", "argocd", "--timeout=" + o.Config.WaitTimeout},
		{"rollout", "status", "deployment/argocd-repo-server", "-n", "argocd", "--timeout=" + o.Config.WaitTimeout},
		{"rollout", "status", "statefulset/argocd-application-controller", "-n", "argocd", "--timeout=" + o.Config.WaitTimeout},
	} {
		if _, err := o.Kube.Kubectl(ctx, nil, args...); err != nil {
			return err
		}
	}
	return nil
}

func (o *RealOperations) waitForPromotedDigests(ctx context.Context) error {
	const (
		appOverlay   = "infrastructure/k8s/apps/coffeeshop/overlays/prod/kustomization.yaml"
		guardOverlay = "platform-ownership-guard/config/prod/kustomization.yaml"
		catalogPath  = "platform/components.yaml"
	)
	return o.waitWithAttempts(ctx, o.Config.ReleaseAttempts, "promoted application and Guard digests", func(ctx context.Context) (bool, error) {
		if _, err := o.Runner.Run(ctx, command.Request{
			Name: "git", Args: []string{"fetch", "--quiet", o.Config.GitOpsRepository, o.Config.GitOpsRevision},
			Dir: o.Config.ProjectRoot, Timeout: 2 * time.Minute,
		}); err != nil {
			return false, nil
		}
		show := func(path string) (string, bool) {
			result, err := o.Runner.Run(ctx, command.Request{
				Name: "git", Args: []string{"show", "FETCH_HEAD:" + path},
				Dir: o.Config.ProjectRoot, Timeout: 30 * time.Second,
			})
			return result.Stdout, err == nil
		}
		appSource, ok := show(appOverlay)
		if !ok {
			return false, nil
		}
		guardSource, ok := show(guardOverlay)
		if !ok {
			return false, nil
		}
		catalogSource, ok := show(catalogPath)
		if !ok {
			return false, nil
		}
		repository, digest, ready := promotedGuardArtifact(appSource, guardSource, catalogSource)
		if !ready {
			return false, nil
		}
		if err := o.AWS.Run(ctx, "ecr", "describe-images",
			"--repository-name", repository,
			"--image-ids", "imageDigest="+digest); err != nil {
			return false, nil
		}
		return true, nil
	})
}

func (o *RealOperations) applyBootstrapManifest(ctx context.Context, name string, replacements map[string]string) error {
	path := filepath.Join(o.Config.ProjectRoot, "infrastructure/k8s/environments/prod/bootstrap", name)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	values := map[string]string{
		"__GITOPS_REPO_URL__": o.Config.GitOpsRepository,
		"__GITOPS_REVISION__": o.Config.GitOpsRevision,
	}
	for key, value := range replacements {
		values[key] = value
	}
	rendered := string(data)
	for key, value := range values {
		rendered = strings.ReplaceAll(rendered, key, value)
	}
	_, err = o.Kube.Kubectl(ctx, strings.NewReader(rendered), "apply", "-f", "-")
	return err
}

func (o *RealOperations) applyRDSBootstrapSecret(ctx context.Context) error {
	secret, err := o.FoundationTF.Output(ctx, "rds_master_secret_arn")
	if err != nil {
		return err
	}
	address, err := o.FoundationTF.Output(ctx, "rds_address")
	if err != nil {
		return err
	}
	port, err := o.FoundationTF.Output(ctx, "rds_port")
	if err != nil {
		return err
	}
	return o.applyBootstrapManifest(ctx, "rds-master-external-secret.yaml.tpl", map[string]string{
		"__RDS_MASTER_SECRET_ARN__": strings.TrimSpace(secret),
		"__RDS_ADDRESS__":           strings.TrimSpace(address),
		"__RDS_PORT__":              strings.TrimSpace(port),
	})
}

func (o *RealOperations) seedApplicationSecret(ctx context.Context) error {
	secretARN, err := o.FoundationTF.Output(ctx, "coffeeshop_app_secret_arn")
	if err != nil {
		return err
	}
	endpoint, err := o.FoundationTF.Output(ctx, "rds_endpoint")
	if err != nil {
		return err
	}
	secretARN = strings.TrimSpace(secretARN)
	endpoint = strings.TrimSpace(endpoint)
	current, _ := o.AWS.Text(ctx, "secretsmanager", "get-secret-value", "--secret-id", secretARN,
		"--query", "SecretString", "--output", "text")
	var existing map[string]string
	if json.Unmarshal([]byte(current), &existing) == nil &&
		len(existing["APP_DB_PASSWORD"]) >= 32 && len(existing["RABBITMQ_DEFAULT_PASS"]) >= 32 &&
		strings.HasPrefix(existing["PG_URL"], "postgres://coffeeshop_app:") {
		return nil
	}
	appPassword, err := randomHex(24)
	if err != nil {
		return err
	}
	rabbitPassword, err := randomHex(24)
	if err != nil {
		return err
	}
	pgURL := "postgres://coffeeshop_app:" + appPassword + "@" + endpoint + "/postgres?sslmode=require"
	rabbitURL := "amqp://coffeeshop:" + rabbitPassword + "@coffeeshop-rabbitmq.coffeeshop.svc.cluster.local:5672/"
	payload, err := json.Marshal(map[string]string{
		"APP_DB_PASSWORD":              appPassword,
		"PG_URL":                       pgURL,
		"PG_DSN_URL":                   pgURL,
		"RABBITMQ_DEFAULT_USER":        "coffeeshop",
		"RABBITMQ_DEFAULT_PASS":        rabbitPassword,
		"RABBITMQ_DEFAULT_USER_CONFIG": "default_user = coffeeshop\ndefault_pass = " + rabbitPassword + "\n",
		"RABBITMQ_URL":                 rabbitURL,
	})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp("", "platformctl-secret-*.json")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(payload); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return o.AWS.Run(ctx, "secretsmanager", "put-secret-value", "--secret-id", secretARN,
		"--secret-string", "file://"+name)
}

func (o *RealOperations) wait(ctx context.Context, description string, observe func(context.Context) (bool, error)) error {
	return o.waitWithAttempts(ctx, o.Config.PollAttempts, description, observe)
}

func (o *RealOperations) waitWithAttempts(ctx context.Context, attempts int, description string, observe func(context.Context) (bool, error)) error {
	return o.Kube.WaitFor(ctx, attempts, 10*time.Second, description, observe)
}

func (o *RealOperations) awsSucceeds(ctx context.Context, args ...string) bool {
	_, err := o.AWS.Text(ctx, args...)
	return err == nil
}

func (o *RealOperations) kubeSucceeds(ctx context.Context, args ...string) bool {
	_, err := o.Kube.Kubectl(ctx, nil, args...)
	return err == nil
}

func (o *RealOperations) helmSucceeds(ctx context.Context, args ...string) bool {
	_, err := o.Kube.Helm(ctx, args...)
	return err == nil
}

func randomHex(bytesCount int) (string, error) {
	value := make([]byte, bytesCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

var teardownTargets = []string{
	"aws_db_instance.postgres",
	"aws_db_subnet_group.rds",
	"aws_security_group.rds",
	"aws_secretsmanager_secret.coffeeshop_app_secret",
	"aws_cloudwatch_log_group.application_logs",
	"aws_cloudwatch_log_group.host_logs",
	"aws_cloudwatch_log_group.dataplane_logs",
	"aws_cloudwatch_log_group.performance_logs",
	"aws_cloudwatch_metric_alarm.rds_free_storage",
	"aws_cloudwatch_metric_alarm.node_cpu_high",
	"aws_eks_addon.ebs_csi",
	"aws_eks_addon.cloudwatch_observability",
	"aws_eks_pod_identity_association.eso",
	"aws_iam_role_policy_attachment.eso_attach",
	"aws_iam_policy.eso_policy",
	"aws_iam_role.eso_role",
	"aws_eks_pod_identity_association.cloudwatch_agent",
	"aws_iam_role_policy_attachment.cloudwatch_agent_attach",
	"aws_iam_role.cloudwatch_agent_role",
	"aws_eks_pod_identity_association.ebs_csi",
	"aws_iam_role_policy_attachment.ebs_csi_attach",
	"aws_iam_role.ebs_csi_role",
	"aws_eks_pod_identity_association.aws_lb_controller",
	"aws_iam_role_policy_attachment.aws_lb_controller_attach",
	"aws_iam_policy.aws_lb_controller_policy",
	"aws_iam_role.aws_lb_controller",
	"module.eks_nodes",
	"aws_eks_addon.coredns",
	"module.eks_cluster",
	"module.vpc",
}
