package prod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/gitops"
	"gopkg.in/yaml.v3"
)

const (
	resilienceNamespace = "coffeeshop"
	resilienceAlarm     = "coffeeshop-prod-resilience-fixture"
	resilienceMetricNS  = "CoffeeShop/Resilience"
	resilienceMetric    = "FailureGate"
)

func (o *RealOperations) runResilience(ctx context.Context) (err error) {
	if err := o.initRemote(ctx, o.FoundationTF, o.Config.FoundationStateKey); err != nil {
		return err
	}
	if err := o.loadRuntimeOutputs(ctx); err != nil {
		return err
	}
	if err := o.updateKubeconfig(ctx); err != nil {
		return err
	}
	defer func() {
		// Cleanup must still run when the operation context was cancelled or
		// timed out; use its own bounded recovery context.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		cleanupErr := o.cleanupResilience(cleanupCtx)
		if err == nil && cleanupErr != nil {
			err = cleanupErr
		}
	}()
	if err := o.verifyArgoApplications(ctx); err != nil {
		return err
	}
	migrationImage, err := o.migrationImage()
	if err != nil {
		return err
	}
	if err := o.resilienceESO(ctx); err != nil {
		return fmt.Errorf("F1 ESO denial: %w", err)
	}
	if err := o.resilienceMigration(ctx, migrationImage); err != nil {
		return fmt.Errorf("F2 migration failure: %w", err)
	}
	if err := o.resilienceDatabaseNetwork(ctx); err != nil {
		return fmt.Errorf("F3 database network denial: %w", err)
	}
	if err := o.resilienceRabbitMQ(ctx); err != nil {
		return fmt.Errorf("F4 RabbitMQ recovery: %w", err)
	}
	if err := o.resilienceCloudWatch(ctx); err != nil {
		return fmt.Errorf("F5 CloudWatch evidence: %w", err)
	}
	return o.verifyArgoApplications(ctx)
}

func (o *RealOperations) resilienceESO(ctx context.Context) error {
	manifest := `apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: resilience-denied-secret
  namespace: coffeeshop
spec:
  refreshInterval: 5s
  secretStoreRef:
    kind: ClusterSecretStore
    name: aws-secretsmanager
  target:
    name: resilience-denied-secret
  data:
    - secretKey: denied
      remoteRef:
        key: /coffeeshop/prod/resilience-denied-fixture
`
	if _, err := o.Kube.Kubectl(ctx, strings.NewReader(manifest), "apply", "-f", "-"); err != nil {
		return err
	}
	if err := o.wait(ctx, "ESO AccessDenied evidence", func(ctx context.Context) (bool, error) {
		reason, _ := o.Kube.Kubectl(ctx, nil, "get", "externalsecret", "resilience-denied-secret",
			"-n", resilienceNamespace, "-o", `jsonpath={.status.conditions[?(@.type=="Ready")].reason}`)
		events, _ := o.Kube.Kubectl(ctx, nil, "get", "events", "-n", resilienceNamespace,
			"--field-selector", "involvedObject.name=resilience-denied-secret",
			"-o", "jsonpath={range .items[*]}{.reason}:{.message}{\"\\n\"}{end}")
		denied := strings.Contains(strings.ToLower(events), "accessdenied") ||
			strings.Contains(strings.ToLower(events), "not authorized")
		return strings.Contains(reason, "SecretSyncedError") && denied, nil
	}); err != nil {
		return err
	}
	if o.kubeSucceeds(ctx, "get", "secret", "resilience-denied-secret", "-n", resilienceNamespace) {
		return fmt.Errorf("ESO materialized an out-of-allowlist Secret")
	}
	_, err := o.Kube.Kubectl(ctx, nil, "delete", "externalsecret", "resilience-denied-secret",
		"-n", resilienceNamespace, "--wait=true")
	return err
}

func (o *RealOperations) resilienceMigration(ctx context.Context, image string) error {
	manifest := fmt.Sprintf(`apiVersion: batch/v1
kind: Job
metadata:
  name: resilience-invalid-migration
  namespace: coffeeshop
spec:
  backoffLimit: 0
  activeDeadlineSeconds: 90
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: migrate
          image: %s
          env:
            - name: MIGRATION_MODE
              value: migrate
            - name: PG_URL
              valueFrom:
                secretKeyRef:
                  name: coffeeshop-secret
                  key: PG_URL
            - name: MIGRATION_PATH
              value: /app/db/does-not-exist
`, image)
	if _, err := o.Kube.Kubectl(ctx, strings.NewReader(manifest), "apply", "-f", "-"); err != nil {
		return err
	}
	if err := o.wait(ctx, "invalid migration failure", func(ctx context.Context) (bool, error) {
		complete, _ := o.Kube.Kubectl(ctx, nil, "get", "job", "resilience-invalid-migration",
			"-n", resilienceNamespace, "-o", `jsonpath={.status.conditions[?(@.type=="Complete")].status}`)
		if complete == "True" {
			return false, fmt.Errorf("invalid migration unexpectedly completed")
		}
		failed, _ := o.Kube.Kubectl(ctx, nil, "get", "job", "resilience-invalid-migration",
			"-n", resilienceNamespace, "-o", `jsonpath={.status.conditions[?(@.type=="Failed")].status}`)
		return failed == "True", nil
	}); err != nil {
		return err
	}
	_, err := o.Kube.Kubectl(ctx, nil, "delete", "job", "resilience-invalid-migration",
		"-n", resilienceNamespace, "--wait=true")
	return err
}

func (o *RealOperations) resilienceDatabaseNetwork(ctx context.Context) error {
	counterPod, err := o.Kube.Kubectl(ctx, nil, "get", "pods", "-n", resilienceNamespace,
		"-l", "app=counter", "-o", "jsonpath={.items[0].metadata.name}")
	if err != nil || counterPod == "" {
		return fmt.Errorf("counter Pod is unavailable")
	}
	hostname, err := o.Kube.Kubectl(ctx, nil, "get", "ingress", "coffeeshop-prod-alb-ingress",
		"-n", resilienceNamespace, "-o", "jsonpath={.status.loadBalancer.ingress[0].hostname}")
	if err != nil || hostname == "" {
		return fmt.Errorf("ALB hostname is unavailable")
	}
	securityGroup, err := o.AWS.Text(ctx, "rds", "describe-db-instances",
		"--db-instance-identifier", o.Config.ProjectName+"-"+o.Config.Environment+"-db",
		"--query", "DBInstances[0].VpcSecurityGroups[0].VpcSecurityGroupId", "--output", "text")
	if err != nil {
		return err
	}
	rdsIP, err := o.AWS.Text(ctx, "ec2", "describe-network-interfaces",
		"--filters", "Name=group-id,Values="+securityGroup, "Name=status,Values=in-use",
		"--query", "NetworkInterfaces[0].PrivateIpAddress", "--output", "text")
	if err != nil {
		return err
	}
	manifest := fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: resilience-db-network-denied
  namespace: coffeeshop
spec:
  podSelector:
    matchLabels:
      app: counter
  policyTypes: [Egress]
  egress:
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except: [%s/32]
`, rdsIP)
	if _, err := o.Kube.Kubectl(ctx, strings.NewReader(manifest), "apply", "-f", "-"); err != nil {
		return err
	}
	if err := o.wait(ctx, "VPC CNI PolicyEndpoint", func(ctx context.Context) (bool, error) {
		data, err := o.Kube.Kubectl(ctx, nil, "get", "policyendpoints.networking.k8s.aws",
			"-n", resilienceNamespace, "-o", "json")
		if err != nil {
			return false, nil
		}
		return policyEndpointContains([]byte(data), "resilience-db-network-denied", counterPod), nil
	}); err != nil {
		return err
	}
	if err := o.wait(ctx, "database-backed order denial", func(ctx context.Context) (bool, error) {
		status, _ := o.postOrder(ctx, hostname, "2026-07-25T00:00:00Z")
		return status < 200 || status >= 300, nil
	}); err != nil {
		return err
	}
	if _, err := o.Kube.Kubectl(ctx, nil, "delete", "networkpolicy", "resilience-db-network-denied",
		"-n", resilienceNamespace, "--wait=true"); err != nil {
		return err
	}
	return o.wait(ctx, "database-backed order recovery", func(ctx context.Context) (bool, error) {
		status, _ := o.postOrder(ctx, hostname, "2026-07-25T00:01:00Z")
		return status >= 200 && status < 300, nil
	})
}

func (o *RealOperations) resilienceRabbitMQ(ctx context.Context) error {
	const pod = "coffeeshop-rabbitmq-server-0"
	if _, err := o.Kube.Kubectl(ctx, nil, "delete", "pod", pod, "-n", resilienceNamespace, "--wait=false"); err != nil {
		return err
	}
	data, err := o.Kube.Kubectl(ctx, nil, "get", "pods", "-n", resilienceNamespace,
		"-l", "app.kubernetes.io/name=coffeeshop-rabbitmq", "-o", "json")
	if err != nil {
		return err
	}
	if readyPeerCount([]byte(data), pod) < 2 {
		return fmt.Errorf("fewer than two RabbitMQ peers remained Ready")
	}
	if _, err := o.Kube.Kubectl(ctx, nil, "wait", "--for=condition=Ready", "pod/"+pod,
		"-n", resilienceNamespace, "--timeout=300s"); err != nil {
		return err
	}
	if err := o.wait(ctx, "RabbitMQ three ready replicas", func(ctx context.Context) (bool, error) {
		ready, _ := o.Kube.Kubectl(ctx, nil, "get", "statefulset", "coffeeshop-rabbitmq-server",
			"-n", resilienceNamespace, "-o", "jsonpath={.status.readyReplicas}")
		return ready == "3", nil
	}); err != nil {
		return err
	}
	_, err = o.Kube.Kubectl(ctx, nil, "exec", pod, "-n", resilienceNamespace, "--",
		"rabbitmq-diagnostics", "-q", "check_running")
	return err
}

func (o *RealOperations) resilienceCloudWatch(ctx context.Context) error {
	streams, err := o.AWS.Text(ctx, "logs", "describe-log-streams",
		"--log-group-name", "/aws/containerinsights/"+o.cluster+"/application",
		"--order-by", "LastEventTime", "--descending", "--limit", "5",
		"--query", "length(logStreams)", "--output", "text")
	if err != nil || streams == "0" {
		return fmt.Errorf("Container Insights application log stream is unavailable")
	}
	logs, err := o.AWS.Text(ctx, "logs", "filter-log-events",
		"--log-group-name", "/aws/containerinsights/"+o.cluster+"/application",
		"--limit", "100", "--query", "events[].message", "--output", "text")
	if err != nil {
		return err
	}
	credentialURI := regexp.MustCompile(`(?i)postgres(?:ql)?://[^\s]+:[^@\s]+@`)
	if credentialURI.MatchString(logs) {
		return fmt.Errorf("sampled application logs contain a credential-bearing PostgreSQL URI")
	}
	metrics, err := o.AWS.Text(ctx, "cloudwatch", "list-metrics",
		"--namespace", "ContainerInsights", "--metric-name", "node_cpu_utilization",
		"--dimensions", "Name=ClusterName,Value="+o.cluster,
		"--query", "length(Metrics)", "--output", "text")
	if err != nil || metrics == "0" {
		return fmt.Errorf("Container Insights node CPU metric is unavailable")
	}
	if err := o.AWS.Run(ctx, "cloudwatch", "put-metric-alarm",
		"--alarm-name", resilienceAlarm,
		"--alarm-description", "Temporary PROD resilience transition fixture",
		"--namespace", resilienceMetricNS, "--metric-name", resilienceMetric,
		"--statistic", "Average", "--period", "10", "--evaluation-periods", "1",
		"--datapoints-to-alarm", "1", "--threshold", "0.5",
		"--comparison-operator", "GreaterThanThreshold", "--treat-missing-data", "notBreaching"); err != nil {
		return err
	}
	for _, transition := range []struct {
		value, expected string
	}{{"0", "OK"}, {"1", "ALARM"}, {"0", "OK"}} {
		if err := o.AWS.Run(ctx, "cloudwatch", "put-metric-data",
			"--namespace", resilienceMetricNS,
			"--metric-data", "MetricName="+resilienceMetric+",Value="+transition.value+",Unit=Count,StorageResolution=1"); err != nil {
			return err
		}
		if err := o.wait(ctx, "CloudWatch alarm "+transition.expected, func(ctx context.Context) (bool, error) {
			state, _ := o.AWS.Text(ctx, "cloudwatch", "describe-alarms",
				"--alarm-names", resilienceAlarm, "--query", "MetricAlarms[0].StateValue", "--output", "text")
			return state == transition.expected, nil
		}); err != nil {
			return err
		}
	}
	return o.AWS.Run(ctx, "cloudwatch", "delete-alarms", "--alarm-names", resilienceAlarm)
}

func (o *RealOperations) verifyArgoApplications(ctx context.Context) error {
	for _, application := range []string{"coffeeshop-prod-platform", "coffeeshop-prod", "coffeeshop-prod-ownership-guard"} {
		data, err := o.Kube.Kubectl(ctx, nil, "get", "application", application,
			"-n", "argocd", "-o", "json")
		if err != nil {
			return err
		}
		if err := gitops.Evaluate([]byte(data)); err != nil {
			return fmt.Errorf("%s: %w", application, err)
		}
	}
	return nil
}

func (o *RealOperations) migrationImage() (string, error) {
	data, err := os.ReadFile(filepath.Join(o.Config.ProjectRoot,
		"infrastructure/k8s/apps/coffeeshop/overlays/prod/kustomization.yaml"))
	if err != nil {
		return "", err
	}
	var document struct {
		Images []struct {
			Name    string `yaml:"name"`
			NewName string `yaml:"newName"`
			Digest  string `yaml:"digest"`
		} `yaml:"images"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return "", err
	}
	for _, image := range document.Images {
		if image.Name == "go-coffeeshop-migrate" && digestPattern.MatchString(image.Digest) {
			return image.NewName + "@" + image.Digest, nil
		}
	}
	return "", fmt.Errorf("immutable migration image is missing")
}

func (o *RealOperations) postOrder(ctx context.Context, hostname, timestamp string) (int, error) {
	payload := `{"loyaltyMemberId":"01234567-89ab-cdef-0123-456789abcdef","timestamp":"` +
		timestamp + `","baristaItems":[{"itemType":0}]}`
	result, err := o.Runner.Run(ctx, command.Request{
		Name: "curl",
		Args: []string{"--silent", "--show-error", "--max-time", "10", "-o", "/dev/null",
			"-w", "%{http_code}", "-H", "Content-Type: application/json", "-d", payload,
			"http://" + hostname + "/api/v1/api/orders"},
		Timeout: 15 * time.Second,
	})
	if err != nil {
		return 0, err
	}
	var status int
	_, err = fmt.Sscanf(strings.TrimSpace(result.Stdout), "%d", &status)
	return status, err
}

func (o *RealOperations) cleanupResilience(ctx context.Context) error {
	var cleanupErrors []error
	for _, args := range [][]string{
		{"delete", "externalsecret", "resilience-denied-secret", "-n", resilienceNamespace, "--ignore-not-found", "--wait=false"},
		{"delete", "secret", "resilience-denied-secret", "-n", resilienceNamespace, "--ignore-not-found", "--wait=false"},
		{"delete", "job", "resilience-invalid-migration", "resilience-db-network-denied", "-n", resilienceNamespace, "--ignore-not-found", "--wait=false"},
		{"delete", "networkpolicy", "resilience-db-network-denied", "-n", resilienceNamespace, "--ignore-not-found", "--wait=false"},
	} {
		if _, err := o.Kube.Kubectl(ctx, nil, args...); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if err := o.AWS.Run(ctx, "cloudwatch", "delete-alarms", "--alarm-names", resilienceAlarm); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	return errors.Join(cleanupErrors...)
}

func policyEndpointContains(data []byte, policy, pod string) bool {
	var document struct {
		Items []struct {
			Spec struct {
				PolicyRef struct {
					Name string `json:"name"`
				} `json:"policyRef"`
				PodIsolation         []string `json:"podIsolation"`
				PodSelectorEndpoints []struct {
					Name string `json:"name"`
				} `json:"podSelectorEndpoints"`
			} `json:"spec"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &document) != nil {
		return false
	}
	for _, item := range document.Items {
		if item.Spec.PolicyRef.Name != policy || !containsString(item.Spec.PodIsolation, "Egress") {
			continue
		}
		for _, endpoint := range item.Spec.PodSelectorEndpoints {
			if endpoint.Name == pod {
				return true
			}
		}
	}
	return false
}

func readyPeerCount(data []byte, excluded string) int {
	var document struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if json.Unmarshal(data, &document) != nil {
		return 0
	}
	count := 0
	for _, item := range document.Items {
		if item.Metadata.Name == excluded {
			continue
		}
		for _, condition := range item.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				count++
				break
			}
		}
	}
	return count
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
