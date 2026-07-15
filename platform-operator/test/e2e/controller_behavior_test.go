//go:build e2e
// +build e2e

package e2e

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/T2Dzung/coffee-shop/platform-operator/test/utils"
)

const (
	behaviorNamespace  = "platform-operator-e2e"
	runtimeServiceName = "phase62-runtime"
	collisionName      = "phase62-collision"
	writeMetricName    = "coffeeshop_operator_write_operations_total"
)

func TestSumWriteMetrics(t *testing.T) {
	fixture := `
coffeeshop_operator_write_operations_total{operation="apply",resource="deployment",result="success"} 1
coffeeshop_operator_write_operations_total{operation="apply",resource="service",result="success"} 2
coffeeshop_operator_write_operations_total{operation="status_patch",resource="coffeeshopservice",result="success"} 2
coffeeshop_operator_write_operations_total{operation="apply",resource="deployment",result="error"} 9
`
	got, err := successfulChildWrites(fixture)
	if err != nil {
		t.Fatalf("parse successful child writes: %v", err)
	}
	if got != 3 {
		t.Fatalf("successful child writes = %v, want 3", got)
	}
	got, err = successfulWrites(fixture)
	if err != nil {
		t.Fatalf("parse all successful writes: %v", err)
	}
	if got != 5 {
		t.Fatalf("all successful writes = %v, want 5", got)
	}
}

func validateControllerBehavior() {
	By("capturing write metrics before the Observe fixture exists")
	metricsBeforeObserve, err := scrapeMetrics()
	Expect(err).NotTo(HaveOccurred())
	childWritesBefore, err := successfulChildWrites(metricsBeforeObserve)
	Expect(err).NotTo(HaveOccurred())

	By("applying an isolated Observe fixture")
	_, err = runKubectl("apply", "-f", "test/fixtures/e2e/runtime.yaml")
	Expect(err).NotTo(HaveOccurred())
	Eventually(readyReason(runtimeServiceName)).Should(Equal("ObserveOnly"))
	Expect(resourceName("deployment", runtimeServiceName)).To(BeEmpty())
	Expect(resourceName("service", runtimeServiceName)).To(BeEmpty())

	By("proving Observe performed zero child writes")
	metricsAfterObserve, err := scrapeMetrics()
	Expect(err).NotTo(HaveOccurred())
	childWritesAfter, err := successfulChildWrites(metricsAfterObserve)
	Expect(err).NotTo(HaveOccurred())
	Expect(childWritesAfter).To(Equal(childWritesBefore))

	By("switching the isolated CR to Manage")
	_, err = runKubectl(
		"patch", "coffeeshopservice", runtimeServiceName,
		"-n", behaviorNamespace, "--type=merge",
		"-p", `{"spec":{"managementPolicy":"Manage"}}`,
	)
	Expect(err).NotTo(HaveOccurred())
	Eventually(ownedChildUID("deployment", runtimeServiceName)).ShouldNot(BeEmpty())
	Eventually(ownedChildUID("service", runtimeServiceName)).ShouldNot(BeEmpty())
	Eventually(readyStatus(runtimeServiceName), 5*time.Minute).Should(Equal("True"))

	By("preserving user metadata and Service allocation across an image update")
	_, err = runKubectl(
		"annotate", "deployment", runtimeServiceName, "-n", behaviorNamespace,
		"example.com/review=preserve-me", "--overwrite",
	)
	Expect(err).NotTo(HaveOccurred())
	clusterIPBefore := jsonPath("service", runtimeServiceName, "{.spec.clusterIP}")
	Expect(clusterIPBefore).NotTo(BeEmpty())
	_, err = runKubectl(
		"patch", "coffeeshopservice", runtimeServiceName,
		"-n", behaviorNamespace, "--type=merge",
		"-p", `{"spec":{"image":{"repository":"nginx","tag":"1.27.4-alpine","pullPolicy":"IfNotPresent"}}}`,
	)
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() string {
		return jsonPath("deployment", runtimeServiceName, "{.spec.template.spec.containers[0].image}")
	}, 5*time.Minute).Should(Equal("nginx:1.27.4-alpine"))
	_, err = runKubectl(
		"rollout", "status", "deployment/"+runtimeServiceName,
		"-n", behaviorNamespace, "--timeout=5m",
	)
	Expect(err).NotTo(HaveOccurred())
	Eventually(readyStatus(runtimeServiceName), 5*time.Minute).Should(Equal("True"))
	Expect(jsonPath("deployment", runtimeServiceName, "{.metadata.annotations.example\\.com/review}")).To(Equal("preserve-me"))
	Expect(jsonPath("service", runtimeServiceName, "{.spec.clusterIP}")).To(Equal(clusterIPBefore))

	By("proving event-driven self-heal after an owned Deployment deletion")
	oldDeploymentUID := jsonPath("deployment", runtimeServiceName, "{.metadata.uid}")
	_, err = runKubectl("delete", "deployment", runtimeServiceName, "-n", behaviorNamespace)
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() string {
		uid := jsonPath("deployment", runtimeServiceName, "{.metadata.uid}")
		if uid == oldDeploymentUID {
			return ""
		}
		return uid
	}, 3*time.Minute).ShouldNot(BeEmpty())
	_, err = runKubectl(
		"rollout", "status", "deployment/"+runtimeServiceName,
		"-n", behaviorNamespace, "--timeout=5m",
	)
	Expect(err).NotTo(HaveOccurred())
	Eventually(readyStatus(runtimeServiceName), 5*time.Minute).Should(Equal("True"))

	By("waiting for the successful-write counter to converge")
	var convergedWrites float64
	Eventually(func(g Gomega) {
		first, scrapeErr := successfulWritesFromEndpoint()
		g.Expect(scrapeErr).NotTo(HaveOccurred())
		time.Sleep(5 * time.Second)
		second, scrapeErr := successfulWritesFromEndpoint()
		g.Expect(scrapeErr).NotTo(HaveOccurred())
		g.Expect(second).To(Equal(first))
		convergedWrites = second
	}, 2*time.Minute, 10*time.Second).Should(Succeed())

	duration := steadyStateDuration()
	By(fmt.Sprintf("proving zero successful write delta during the %s steady-state window", duration))
	Consistently(successfulWritesFromEndpoint, duration, 15*time.Second).Should(Equal(convergedWrites))

	By("proving ownerReference garbage collection with the real controller-manager")
	_, err = runKubectl("delete", "coffeeshopservice", runtimeServiceName, "-n", behaviorNamespace)
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() string { return resourceName("deployment", runtimeServiceName) }, 3*time.Minute).Should(BeEmpty())
	Eventually(func() string { return resourceName("service", runtimeServiceName) }, 3*time.Minute).Should(BeEmpty())

	By("proving an unowned collision is neither mutated nor garbage collected")
	_, err = runKubectl("apply", "-f", "test/fixtures/e2e/collision.yaml")
	Expect(err).NotTo(HaveOccurred())
	collisionUID := jsonPath("deployment", collisionName, "{.metadata.uid}")
	Eventually(readyReason(collisionName)).Should(Equal("OwnershipConflict"))
	Expect(jsonPath("deployment", collisionName, "{.metadata.uid}")).To(Equal(collisionUID))
	Expect(jsonPath("deployment", collisionName, "{.metadata.ownerReferences}")).To(BeEmpty())
	Expect(jsonPath("deployment", collisionName, "{.metadata.annotations.example\\.com/owner}")).To(Equal("external-platform"))
	_, err = runKubectl("delete", "coffeeshopservice", collisionName, "-n", behaviorNamespace)
	Expect(err).NotTo(HaveOccurred())
	Consistently(func() string { return jsonPath("deployment", collisionName, "{.metadata.uid}") }, 15*time.Second).Should(Equal(collisionUID))
	_, err = runKubectl("delete", "deployment", collisionName, "-n", behaviorNamespace)
	Expect(err).NotTo(HaveOccurred())
}

func runKubectl(args ...string) (string, error) {
	return utils.Run(exec.Command("kubectl", args...))
}

func resourceName(kind, name string) string {
	output, err := runKubectl("get", kind, name, "-n", behaviorNamespace, "--ignore-not-found", "-o", "name")
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return strings.TrimSpace(output)
}

func jsonPath(kind, name, path string) string {
	output, err := runKubectl("get", kind, name, "-n", behaviorNamespace, "-o", "jsonpath="+path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(output)
}

func readyReason(name string) func() string {
	return func() string {
		return jsonPath("coffeeshopservice", name, `{.status.conditions[?(@.type=="Ready")].reason}`)
	}
}

func readyStatus(name string) func() string {
	return func() string {
		return jsonPath("coffeeshopservice", name, `{.status.conditions[?(@.type=="Ready")].status}`)
	}
}

func ownedChildUID(kind, name string) func() string {
	return func() string {
		parentUID := jsonPath("coffeeshopservice", name, "{.metadata.uid}")
		ownerUID := jsonPath(kind, name, "{.metadata.ownerReferences[0].uid}")
		if parentUID == "" || ownerUID != parentUID {
			return ""
		}
		return ownerUID
	}
}

func successfulWritesFromEndpoint() (float64, error) {
	output, err := scrapeMetrics()
	if err != nil {
		return 0, err
	}
	return successfulWrites(output)
}

func successfulWrites(metrics string) (float64, error) {
	return sumWriteMetrics(metrics, false)
}

func successfulChildWrites(metrics string) (float64, error) {
	return sumWriteMetrics(metrics, true)
}

func sumWriteMetrics(metrics string, childrenOnly bool) (float64, error) {
	var total float64
	scanner := bufio.NewScanner(strings.NewReader(metrics))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, writeMetricName+"{") || !strings.Contains(line, `result="success"`) {
			continue
		}
		if childrenOnly && !strings.Contains(line, `resource="deployment"`) &&
			!strings.Contains(line, `resource="service"`) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return 0, fmt.Errorf("parse metric value %q: %w", fields[1], err)
		}
		total += value
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan metrics: %w", err)
	}
	return total, nil
}

func steadyStateDuration() time.Duration {
	const defaultDuration = 10 * time.Minute
	value := os.Getenv("E2E_STEADY_STATE_DURATION")
	if value == "" {
		return defaultDuration
	}
	duration, err := time.ParseDuration(value)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return duration
}
