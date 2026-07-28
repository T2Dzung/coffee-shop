package prod

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

type loadBalancersDocument struct {
	LoadBalancers []struct {
		ARN   string `json:"LoadBalancerArn"`
		DNS   string `json:"DNSName"`
		Type  string `json:"Type"`
		State struct {
			Code string `json:"Code"`
		} `json:"State"`
		AvailabilityZones []struct {
			Zone string `json:"ZoneName"`
		} `json:"AvailabilityZones"`
	} `json:"LoadBalancers"`
}

type targetGroupsDocument struct {
	TargetGroups []struct {
		ARN        string `json:"TargetGroupArn"`
		TargetType string `json:"TargetType"`
		Port       int    `json:"Port"`
	} `json:"TargetGroups"`
}

type targetHealthDocument struct {
	Descriptions []struct {
		Target struct {
			ID   string `json:"Id"`
			Zone string `json:"AvailabilityZone"`
		} `json:"Target"`
		Health struct {
			State string `json:"State"`
		} `json:"TargetHealth"`
	} `json:"TargetHealthDescriptions"`
}

type podListDocument struct {
	Items []struct {
		Status struct {
			PodIP      string `json:"podIP"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
			ContainerStatuses []struct {
				Name    string `json:"name"`
				ImageID string `json:"imageID"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

func (o *RealOperations) verifyIngressAndTransaction(ctx context.Context) error {
	hostname, err := o.Kube.Kubectl(ctx, nil, "get", "ingress", "coffeeshop-prod-alb-ingress",
		"-n", "coffeeshop", "-o", "jsonpath={.status.loadBalancer.ingress[0].hostname}")
	if err != nil || hostname == "" {
		return fmt.Errorf("PROD ALB hostname is unavailable")
	}

	var lbARN string
	var zones []string
	if err := o.wait(ctx, "application load balancer "+hostname+" to become active", func(ctx context.Context) (bool, error) {
		var lbs loadBalancersDocument
		if err := o.AWS.JSON(ctx, &lbs, "elbv2", "describe-load-balancers", "--output", "json"); err != nil {
			return false, err
		}
		arn, observedZones, ready, err := activeApplicationLoadBalancer(lbs, hostname)
		if err != nil {
			return false, err
		}
		if ready {
			lbARN = arn
			zones = observedZones
		}
		return ready, nil
	}); err != nil {
		return err
	}
	slices.Sort(zones)
	zones = slices.Compact(zones)
	if len(zones) < 2 {
		return fmt.Errorf("ALB spans only %d Availability Zone(s)", len(zones))
	}

	servicePortText, err := o.Kube.Kubectl(ctx, nil, "get", "service", "web", "-n", "coffeeshop",
		"-o", "jsonpath={.spec.ports[0].port}")
	if err != nil {
		return err
	}
	servicePort, err := strconv.Atoi(servicePortText)
	if err != nil {
		return fmt.Errorf("invalid web Service port %q", servicePortText)
	}
	var groups targetGroupsDocument
	if err := o.AWS.JSON(ctx, &groups, "elbv2", "describe-target-groups",
		"--load-balancer-arn", lbARN, "--output", "json"); err != nil {
		return err
	}
	var targetGroupARN string
	for _, group := range groups.TargetGroups {
		if group.TargetType == "ip" && group.Port == servicePort {
			if targetGroupARN != "" {
				return fmt.Errorf("multiple IP target groups use web Service port %d", servicePort)
			}
			targetGroupARN = group.ARN
		}
	}
	if targetGroupARN == "" {
		return fmt.Errorf("no IP target group uses web Service port %d", servicePort)
	}

	var listeners struct {
		Listeners []struct {
			Port     int    `json:"Port"`
			Protocol string `json:"Protocol"`
		} `json:"Listeners"`
	}
	if err := o.AWS.JSON(ctx, &listeners, "elbv2", "describe-listeners",
		"--load-balancer-arn", lbARN, "--output", "json"); err != nil {
		return err
	}
	httpListeners := 0
	for _, listener := range listeners.Listeners {
		if listener.Port == 80 && listener.Protocol == "HTTP" {
			httpListeners++
		}
	}
	if httpListeners != 1 {
		return fmt.Errorf("expected exactly one HTTP:80 listener, found %d", httpListeners)
	}

	var health targetHealthDocument
	if err := o.AWS.JSON(ctx, &health, "elbv2", "describe-target-health",
		"--target-group-arn", targetGroupARN, "--output", "json"); err != nil {
		return err
	}
	var healthyIPs []string
	for _, target := range health.Descriptions {
		if target.Health.State == "healthy" {
			healthyIPs = append(healthyIPs, target.Target.ID)
		}
	}
	if len(healthyIPs) == 0 {
		return fmt.Errorf("ALB target group has no healthy target")
	}
	readyPods, err := o.webPods(ctx)
	if err != nil {
		return err
	}
	var readyIPs []string
	for _, pod := range readyPods.Items {
		if podReady(pod.Status.Conditions) {
			readyIPs = append(readyIPs, pod.Status.PodIP)
		}
	}
	slices.Sort(healthyIPs)
	slices.Sort(readyIPs)
	if !slices.Equal(healthyIPs, readyIPs) {
		return fmt.Errorf("healthy ALB target IPs %v do not match Ready web Pod IPs %v", healthyIPs, readyIPs)
	}

	baseURL := "http://" + hostname
	items, err := o.http(ctx, "GET", baseURL+"/api/v1/api/item-types", "")
	if err != nil {
		return fmt.Errorf("load item types through ALB: %w", err)
	}
	var itemTypes struct {
		Items []any `json:"itemTypes"`
	}
	if json.Unmarshal([]byte(items), &itemTypes) != nil || len(itemTypes.Items) == 0 {
		return fmt.Errorf("item-types probe returned no usable product data")
	}
	orderBody := `{"loyaltyMemberId":"01234567-89ab-cdef-0123-456789abcdef","timestamp":"2026-07-25T00:00:00Z","baristaItems":[{"itemType":0}]}`
	order, err := o.http(ctx, "POST", baseURL+"/api/v1/api/orders", orderBody)
	if err != nil {
		return fmt.Errorf("create order through ALB: %w", err)
	}
	var orderObject map[string]any
	if json.Unmarshal([]byte(order), &orderObject) != nil || orderObject == nil {
		return fmt.Errorf("order probe returned a non-object response")
	}
	fmt.Fprintf(o.Output, "Runtime ingress passed: %d healthy target(s), %d ALB AZ(s), transaction probe succeeded.\n",
		len(healthyIPs), len(zones))
	return nil
}

func activeApplicationLoadBalancer(lbs loadBalancersDocument, hostname string) (string, []string, bool, error) {
	var arn string
	var zones []string
	for _, lb := range lbs.LoadBalancers {
		if lb.DNS != hostname || lb.Type != "application" || lb.State.Code != "active" {
			continue
		}
		if arn != "" {
			return "", nil, false, fmt.Errorf("multiple active ALBs resolve to %s", hostname)
		}
		arn = lb.ARN
		for _, zone := range lb.AvailabilityZones {
			zones = append(zones, zone.Zone)
		}
	}
	return arn, zones, arn != "", nil
}

func (o *RealOperations) http(ctx context.Context, method, endpoint, body string) (string, error) {
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return "", err
	}
	args := []string{"--fail", "--silent", "--show-error", "--max-time", "15", "--request", method}
	if body != "" {
		args = append(args, "--header", "Content-Type: application/json", "--data", body)
	}
	args = append(args, endpoint)
	result, err := o.Runner.Run(ctx, command.Request{Name: "curl", Args: args, Timeout: 20 * time.Second})
	return result.Stdout, err
}

func (o *RealOperations) webPods(ctx context.Context) (podListDocument, error) {
	data, err := o.Kube.Kubectl(ctx, nil, "get", "pods", "-n", "coffeeshop", "-l", "app=web", "-o", "json")
	if err != nil {
		return podListDocument{}, err
	}
	var pods podListDocument
	if err := json.Unmarshal([]byte(data), &pods); err != nil {
		return podListDocument{}, fmt.Errorf("decode web Pods: %w", err)
	}
	return pods, nil
}

func podReady(conditions []struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}) bool {
	for _, condition := range conditions {
		if condition.Type == "Ready" && condition.Status == "True" {
			return true
		}
	}
	return false
}

func (o *RealOperations) verifyArgoSelfHeal(ctx context.Context) error {
	desired, err := o.Kube.Kubectl(ctx, nil, "get", "deployment", "web", "-n", "coffeeshop",
		"-o", "jsonpath={.spec.replicas}")
	if err != nil || desired == "" || desired == "0" {
		return fmt.Errorf("cannot establish non-zero web replica baseline")
	}
	if _, err := o.Kube.Kubectl(ctx, nil, "patch", "deployment", "web", "-n", "coffeeshop",
		"--type=merge", "-p", `{"spec":{"replicas":0}}`); err != nil {
		return err
	}
	if err := o.wait(ctx, "Argo CD web replica self-heal", func(ctx context.Context) (bool, error) {
		current, err := o.Kube.Kubectl(ctx, nil, "get", "deployment", "web", "-n", "coffeeshop",
			"-o", "jsonpath={.spec.replicas}")
		return err == nil && current == desired, nil
	}); err != nil {
		return err
	}
	_, err = o.Kube.Kubectl(ctx, nil, "rollout", "status", "deployment/web", "-n", "coffeeshop",
		"--timeout="+o.Config.WaitTimeout)
	return err
}

func runningWebDigest(pods podListDocument) (string, error) {
	digests := map[string]struct{}{}
	for _, pod := range pods.Items {
		if !podReady(pod.Status.Conditions) {
			continue
		}
		for _, container := range pod.Status.ContainerStatuses {
			if container.Name != "web" {
				continue
			}
			_, digest, found := strings.Cut(container.ImageID, "@")
			if !found || !digestPattern.MatchString(digest) {
				return "", fmt.Errorf("invalid running web imageID %q", container.ImageID)
			}
			digests[digest] = struct{}{}
		}
	}
	if len(digests) != 1 {
		return "", fmt.Errorf("Ready web Pods run %d distinct digests", len(digests))
	}
	for digest := range digests {
		return digest, nil
	}
	return "", fmt.Errorf("no Ready web Pod digest found")
}
