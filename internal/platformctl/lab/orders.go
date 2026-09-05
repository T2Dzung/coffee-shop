package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const orderFixtureMember = "33333333-3333-4333-8333-333333333333"

func (e Engine) ordersPresent(ctx context.Context) (bool, error) {
	present, err := e.statefulPresent(ctx)
	if err != nil || !present {
		return false, err
	}
	raw, err := e.Client.Remote(ctx, "kubectl", "-n", dataNamespace, "get", "configmap", "orders-installed", "--ignore-not-found", "-o", "name")
	return strings.TrimSpace(raw) != "", err
}

func (e Engine) orders(ctx context.Context) error {
	present, err := e.statefulPresent(ctx)
	if err != nil {
		return err
	}
	if !present {
		return fmt.Errorf("orders requires the stateful profile")
	}
	marker, err := e.Client.Remote(ctx, "kubectl", "-n", dataNamespace, "get", "configmap", "stateful-installed", "--ignore-not-found", "-o", "name")
	if err != nil {
		return err
	}
	if strings.TrimSpace(marker) == "" {
		return fmt.Errorf("orders requires a completed stateful profile")
	}
	installed, err := e.ordersPresent(ctx)
	if err != nil {
		return err
	}
	if installed {
		return e.ordersRecoverAndVerify(ctx)
	}

	bundle, err := sourceBundle(e.Config.Root)
	if err != nil {
		return err
	}
	if err = e.step("orders-source", func() error {
		_, err := e.Client.RemoteInput(ctx, bytes.NewReader(bundle), "tar", "-xf", "-", "--no-same-owner", "-C", e.Config.Workspace)
		return err
	}); err != nil {
		return err
	}
	if err = e.buildProfile(ctx, e.Config.OrderComponents, e.Config.OrderComponentImages, "runtime-orders", "iximiuz-orders"); err != nil {
		return err
	}
	images, err := e.cacheProfile(ctx, "runtime-orders", e.Config.OrderImages)
	if err != nil {
		return err
	}
	if err = e.step("orders-registry", func() error { return e.recoverImages(ctx, images, e.Config.OrderImages) }); err != nil {
		return err
	}
	if err = e.step("orders-migration", func() error {
		raw, migrationErr := e.Client.Remote(ctx, "kubectl", "-n", dataNamespace, "get", "job", "coffeeshop-orders-migration", "--ignore-not-found", "-o", "json")
		if migrationErr != nil {
			return migrationErr
		}
		exists, complete, failed, migrationErr := migrationJobState(raw)
		if migrationErr != nil {
			return migrationErr
		}
		if failed {
			return fmt.Errorf("migration Job failed; inspect it before deleting the exact Job for retry")
		}
		if complete {
			return nil
		}
		if !exists {
			manifest, buildErr := e.orderMigrationManifest(images)
			if buildErr != nil {
				return buildErr
			}
			if _, buildErr = e.Client.RemoteInput(ctx, bytes.NewReader(manifest), "kubectl", "apply", "-f", "-"); buildErr != nil {
				return buildErr
			}
		}
		_, migrationErr = e.Client.Remote(ctx, "kubectl", "-n", dataNamespace, "wait", "job/coffeeshop-orders-migration", "--for=condition=Complete", "--timeout=300s")
		return migrationErr
	}); err != nil {
		return err
	}
	if err = e.step("orders-rollout", func() error {
		for _, args := range [][]string{
			{"kubectl", "apply", "-k", path.Join(e.Config.Workspace, "runtime-orders")},
			// Re-render core after source sync so proxy points at the optional counter FQDN.
			{"kubectl", "apply", "-k", path.Join(e.Config.Workspace, "runtime-core")},
		} {
			if _, applyErr := e.Client.Remote(ctx, args...); applyErr != nil {
				return applyErr
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err = e.ordersReady(ctx); err != nil {
		return err
	}
	if err = e.placeOrderFixture(ctx); err != nil {
		return err
	}
	if err = e.ordersVerifyData(ctx); err != nil {
		return err
	}
	markerManifest := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: orders-installed\n  namespace: " + dataNamespace + "\n  labels:\n    app.kubernetes.io/managed-by: platformctl-lab\ndata:\n  profile: orders-v1\n")
	_, err = e.Client.RemoteInput(ctx, bytes.NewReader(markerManifest), "kubectl", "apply", "-f", "-")
	return err
}

func migrationJobState(raw string) (exists, complete, failed bool, err error) {
	if strings.TrimSpace(raw) == "" {
		return false, false, false, nil
	}
	var job struct {
		Status struct {
			Conditions []struct {
				Type, Status string
			} `json:"conditions"`
		} `json:"status"`
	}
	if err = json.Unmarshal([]byte(raw), &job); err != nil {
		return true, false, false, fmt.Errorf("decode migration Job: %w", err)
	}
	for _, condition := range job.Status.Conditions {
		if condition.Status != "True" {
			continue
		}
		complete = complete || condition.Type == "Complete"
		failed = failed || condition.Type == "Failed"
	}
	return true, complete, failed, nil
}

func (e Engine) orderMigrationManifest(images []Image) ([]byte, error) {
	var migration Image
	for _, image := range images {
		if image.NewName == registry+"/go-coffeeshop-migrate" {
			migration = image
		}
	}
	if migration.Digest == "" {
		return nil, fmt.Errorf("migration digest is absent")
	}
	data, err := os.ReadFile(filepath.Join(e.Config.Root, "infrastructure/k8s/apps/coffeeshop/overlays/iximiuz-orders/migration-job.yaml"))
	if err != nil {
		return nil, err
	}
	old := "image: go-coffeeshop-migrate"
	if bytes.Count(data, []byte(old)) != 1 {
		return nil, fmt.Errorf("migration image placeholder must occur once")
	}
	data = bytes.Replace(data, []byte(old), []byte("image: "+migration.NewName+"@"+migration.Digest), 1)
	var doc struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
	}
	if err = yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc.Kind != "Job" || doc.Metadata.Name != "coffeeshop-orders-migration" || doc.Metadata.Namespace != dataNamespace {
		return nil, fmt.Errorf("migration job escaped the lab boundary")
	}
	return data, nil
}

func (e Engine) ordersRecoverAndVerify(ctx context.Context) error {
	images, err := e.cacheProfile(ctx, "runtime-orders", e.Config.OrderImages)
	if err != nil {
		return err
	}
	if err = e.step("orders-registry", func() error { return e.recoverImages(ctx, images, e.Config.OrderImages) }); err != nil {
		return err
	}
	if err = e.ordersReady(ctx); err != nil {
		return err
	}
	return e.ordersVerifyData(ctx)
}

func (e Engine) ordersReady(ctx context.Context) error {
	return e.step("orders-ready", func() error {
		for _, name := range []string{"counter", "barista", "kitchen"} {
			if _, err := e.Client.Remote(ctx, "kubectl", "-n", dataNamespace, "rollout", "status", "deployment/"+name, "--timeout=180s"); err != nil {
				return err
			}
		}
		raw, err := e.rabbitRequest(ctx, "GET", "/queues/%2F", "")
		if err != nil {
			return err
		}
		var queues []struct {
			Name    string `json:"name"`
			Durable bool   `json:"durable"`
		}
		if err = json.Unmarshal([]byte(raw), &queues); err != nil {
			return err
		}
		required := map[string]bool{"counter-order-queue": false, "barista-order-queue": false, "kitchen-order-queue": false}
		for _, queue := range queues {
			if _, ok := required[queue.Name]; ok && queue.Durable {
				required[queue.Name] = true
			}
		}
		for name, ready := range required {
			if !ready {
				return fmt.Errorf("durable application queue %s is missing", name)
			}
		}
		return nil
	})
}

func (e Engine) placeOrderFixture(ctx context.Context) error {
	return e.step("orders-place", func() error {
		count, err := e.postgresSQL(ctx, "SELECT count(*) FROM \"order\".orders WHERE loyalty_member_id='"+orderFixtureMember+"';")
		if err != nil {
			return err
		}
		if strings.TrimSpace(count) != "0" {
			return nil
		}
		payload := `{"command_type":0,"order_source":1,"location":0,"loyalty_member_id":"` + orderFixtureMember + `","barista_items":[{"item_type":0}],"kitchen_items":[{"item_type":6}],"timestamp":"2026-09-05T00:00:00Z"}`
		_, err = e.Client.Remote(ctx, "kubectl", "-n", "coffeeshop-lab-checks", "exec", "smoke-client", "--", "curl", "--fail", "--silent", "--show-error", "--max-time", "20", "-H", "Content-Type: application/json", "--data-binary", payload, "http://proxy.coffeeshop-lab.svc.cluster.local:5000/api/v1/api/orders")
		return err
	})
}

func (e Engine) ordersVerifyData(ctx context.Context) error {
	return e.step("orders-data", func() error {
		query := "SELECT (SELECT count(*) FROM \"order\".orders WHERE loyalty_member_id='" + orderFixtureMember + "'), (SELECT count(*) FROM barista.barista_orders), (SELECT count(*) FROM kitchen.kitchen_orders);"
		var last string
		for attempt := 0; attempt < 20; attempt++ {
			raw, err := e.postgresSQL(ctx, query)
			if err != nil {
				return err
			}
			last = strings.TrimSpace(raw)
			parts := strings.Split(last, "|")
			if len(parts) == 3 {
				ok := true
				for _, part := range parts {
					n, parseErr := strconv.Atoi(part)
					ok = ok && parseErr == nil && n >= 1
				}
				if ok {
					return nil
				}
			}
			time.Sleep(2 * time.Second)
		}
		return fmt.Errorf("order pipeline did not persist all three stages; counts=%s", last)
	})
}
