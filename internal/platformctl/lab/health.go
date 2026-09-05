package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/evidence"
)

func (e Engine) ready(ctx context.Context) error {
	checks := [][]string{{"kubectl", "wait", "node", "--all", "--for=condition=Ready", "--timeout=60s"},
		{"kubectl", "-n", "kube-system", "rollout", "status", "ds/cilium", "--timeout=60s"},
		{"kubectl", "-n", "kube-system", "rollout", "status", "deployment/hubble-relay", "--timeout=60s"}}
	for _, name := range e.Config.Components {
		checks = append(checks, []string{"kubectl", "-n", e.Config.Namespace, "wait", "pod", "-l", "app=" + name, "--for=condition=Ready", "--timeout=60s"})
	}
	checks = append(checks, []string{"kubectl", "-n", "coffeeshop-lab-checks", "wait", "pod/smoke-client", "--for=condition=Ready", "--timeout=60s"})
	for _, args := range checks {
		if _, err := e.Client.Remote(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

func (e Engine) smoke(ctx context.Context) error {
	data, err := e.Client.Remote(ctx, "curl", "-fsS", "--max-time", "10", e.Config.MenuURL)
	if err != nil {
		return err
	}
	if err := checkMenu(data); err != nil {
		return err
	}
	data, err = e.Client.Remote(ctx, "kubectl", "-n", e.Config.Namespace, "get", "service", "proxy", "-o", "json")
	if err != nil {
		return err
	}
	var svc struct {
		Spec struct {
			Ports []struct {
				Port int `json:"port"`
			} `json:"ports"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(data), &svc); err != nil {
		return fmt.Errorf("decode proxy service: %w", err)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port < 1 || svc.Spec.Ports[0].Port > 65535 {
		return fmt.Errorf("invalid proxy service port")
	}
	url := "http://proxy." + e.Config.Namespace + ".svc.cluster.local:" + strconv.Itoa(svc.Spec.Ports[0].Port) + "/api/v1/api/item-types"
	data, err = e.Client.Remote(ctx, "kubectl", "-n", "coffeeshop-lab-checks", "exec", "smoke-client", "--", "curl", "-fsS", "--max-time", "10", url)
	if err != nil {
		return err
	}
	if err := checkMenu(data); err != nil {
		return err
	}
	data, err = e.Client.Remote(ctx, "kubectl", "-n", e.Config.Namespace, "get", "gateway", e.Config.Gateway, "-o", "json")
	if err != nil {
		return err
	}
	var gateway struct {
		Metadata struct {
			Generation int64 `json:"generation"`
		} `json:"metadata"`
		Status struct {
			Conditions []struct {
				Type, Status, Reason string
				ObservedGeneration   int64 `json:"observedGeneration"`
			} `json:"conditions"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(data), &gateway); err != nil {
		return fmt.Errorf("decode gateway: %w", err)
	}
	accepted := false
	for _, condition := range gateway.Status.Conditions {
		if condition.Type == "Accepted" && condition.Status == "True" && condition.ObservedGeneration == gateway.Metadata.Generation {
			accepted = true
		}
	}
	if !accepted {
		return fmt.Errorf("Gateway is not accepted at its current generation")
	}
	for _, condition := range gateway.Status.Conditions {
		if condition.Type != "Programmed" {
			continue
		}
		if condition.ObservedGeneration != gateway.Metadata.Generation {
			return fmt.Errorf("Gateway status is stale")
		}
		if condition.Status == "True" {
			return nil
		}
		if condition.Status == "False" && condition.Reason == "AddressNotAssigned" {
			fmt.Fprintln(e.Output, "WARNING: Gateway datapath smoke passed, but Programmed=False/AddressNotAssigned (known host-network limitation).")
			e.Evidence.Record(evidence.Event{Phase: "lab", Step: "gateway-status", Status: "warning", Details: map[string]any{"reason": "AddressNotAssigned", "http_smoke": "passed"}})
			return nil
		}
		return fmt.Errorf("Gateway is not programmed: %s", condition.Reason)
	}
	return fmt.Errorf("Gateway has no Programmed condition")
}

func checkMenu(data string) error {
	var menu struct {
		Items []json.RawMessage `json:"itemTypes"`
	}
	if err := json.Unmarshal([]byte(data), &menu); err != nil {
		return fmt.Errorf("decode menu: %w", err)
	}
	if len(menu.Items) == 0 {
		return fmt.Errorf("menu is empty")
	}
	return nil
}
