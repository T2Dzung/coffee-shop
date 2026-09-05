package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const persistenceValue = "coffeeshop-lab-stateful-v1"
const fixtureQueue = "platformctl-persistence-check"
const rabbitAPI = "http://coffeeshop-rabbitmq:15672/api"

func (e Engine) statefulReady(ctx context.Context) error {
	e.Client.RemoteTimeout = 8 * time.Minute
	return e.step("stateful-ready", func() error {
		for _, args := range [][]string{
			{"kubectl", "-n", dataNamespace, "wait", "cluster.postgresql.cnpg.io/coffeeshop-postgres", "--for=condition=Ready", "--timeout=300s"},
			{"kubectl", "-n", dataNamespace, "wait", "rabbitmqcluster/coffeeshop-rabbitmq", "--for=condition=AllReplicasReady", "--timeout=300s"},
			{"kubectl", "-n", dataNamespace, "rollout", "status", "statefulset/coffeeshop-rabbitmq-server", "--timeout=300s"},
			{"kubectl", "-n", dataNamespace, "wait", "pod/stateful-checker", "--for=condition=Ready", "--timeout=120s"},
		} {
			if _, err := e.Client.Remote(ctx, args...); err != nil {
				return err
			}
		}
		raw, err := e.rabbitRequest(ctx, "GET", "/nodes", "")
		if err != nil {
			return err
		}
		var nodes []struct {
			Running bool `json:"running"`
		}
		if err = json.Unmarshal([]byte(raw), &nodes); err != nil {
			return err
		}
		if len(nodes) != 3 {
			return fmt.Errorf("RabbitMQ expected 3 joined nodes, got %d", len(nodes))
		}
		for _, n := range nodes {
			if !n.Running {
				return fmt.Errorf("RabbitMQ node not running")
			}
		}
		return nil
	})
}

func (e Engine) postgresPrimary(ctx context.Context) (string, error) {
	raw, err := e.Client.Remote(ctx, "kubectl", "-n", dataNamespace, "get", "cluster.postgresql.cnpg.io", "coffeeshop-postgres", "-o", "json")
	if err != nil {
		return "", err
	}
	var cluster struct {
		Status struct {
			CurrentPrimary string `json:"currentPrimary"`
			ReadyInstances int    `json:"readyInstances"`
		} `json:"status"`
	}
	if err = json.Unmarshal([]byte(raw), &cluster); err != nil {
		return "", err
	}
	if cluster.Status.ReadyInstances != 2 || !regexp.MustCompile(`^coffeeshop-postgres-[0-9]+$`).MatchString(cluster.Status.CurrentPrimary) {
		return "", fmt.Errorf("PostgreSQL needs 2 ready instances and a known primary")
	}
	return cluster.Status.CurrentPrimary, nil
}

func (e Engine) postgresSQL(ctx context.Context, sql string) (string, error) {
	pod, err := e.postgresPrimary(ctx)
	if err != nil {
		return "", err
	}
	return e.Client.Remote(ctx, "kubectl", "-n", dataNamespace, "exec", pod, "-c", "postgres", "--", "psql", "-U", "postgres", "-d", "coffeeshop", "-v", "ON_ERROR_STOP=1", "-At", "-c", sql)
}

func (e Engine) rabbitRequest(ctx context.Context, method, suffix, body string) (string, error) {
	leaf, err := os.ReadFile(filepath.Join(e.Config.Root, statefulDir, "rabbit-request.sh"))
	if err != nil {
		return "", err
	}
	args := []string{"kubectl", "-n", dataNamespace, "exec", "-i", "stateful-checker", "--", "sh", "-s", "--", "-X", method, rabbitAPI + suffix}
	if body != "" {
		args = append(args, "-H", "Content-Type: application/json", "--data-binary", body)
	}
	return e.Client.RemoteInput(ctx, bytes.NewReader(leaf), args...)
}

// Only explicit installation seeds a new fixture. Resume never inserts missing
// data, so a failed persistence check cannot be hidden by reinitialization.
func (e Engine) statefulSeed(ctx context.Context) error {
	return e.step("stateful-seed", func() error {
		_, err := e.postgresSQL(ctx, "CREATE SCHEMA IF NOT EXISTS platformctl_lab; CREATE TABLE IF NOT EXISTS platformctl_lab.persistence_check (id integer PRIMARY KEY, value text NOT NULL); INSERT INTO platformctl_lab.persistence_check VALUES (1, '"+persistenceValue+"') ON CONFLICT (id) DO NOTHING;")
		if err != nil {
			return err
		}
		if _, err = e.rabbitRequest(ctx, "PUT", "/queues/%2F/"+fixtureQueue, `{"durable":true,"auto_delete":false,"arguments":{"x-queue-type":"quorum"}}`); err != nil {
			return err
		}
		present, err := e.queueFixturePresent(ctx)
		if err != nil {
			return err
		}
		if present {
			return nil
		}
		raw, err := e.rabbitRequest(ctx, "POST", "/exchanges/%2F/amq.default/publish", `{"properties":{"delivery_mode":2},"routing_key":"`+fixtureQueue+`","payload":"`+persistenceValue+`","payload_encoding":"string"}`)
		if err != nil {
			return err
		}
		var result struct {
			Routed bool `json:"routed"`
		}
		if err = json.Unmarshal([]byte(raw), &result); err != nil {
			return err
		}
		if !result.Routed {
			return fmt.Errorf("persistence message not routed")
		}
		return nil
	})
}

func (e Engine) statefulVerifyData(ctx context.Context) error {
	return e.step("stateful-data", func() error {
		value, err := e.postgresSQL(ctx, "SELECT value FROM platformctl_lab.persistence_check WHERE id=1;")
		if err != nil {
			return err
		}
		if strings.TrimSpace(value) != persistenceValue {
			return fmt.Errorf("PostgreSQL persistence fixture missing or changed; not reseeding")
		}
		present, err := e.queueFixturePresent(ctx)
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("RabbitMQ persistence fixture missing or changed; not reseeding")
		}
		return nil
	})
}

func (e Engine) queueFixturePresent(ctx context.Context) (bool, error) {
	raw, err := e.rabbitRequest(ctx, "POST", "/queues/%2F/"+fixtureQueue+"/get", `{"count":1,"ackmode":"ack_requeue_true","encoding":"auto","truncate":1024}`)
	if err != nil {
		return false, err
	}
	var messages []struct {
		Payload string `json:"payload"`
	}
	if err = json.Unmarshal([]byte(raw), &messages); err != nil {
		return false, err
	}
	if len(messages) == 0 {
		return false, nil
	}
	if len(messages) != 1 || messages[0].Payload != persistenceValue {
		return false, fmt.Errorf("unexpected fixture payload; refusing to overwrite or reseed")
	}
	return true, nil
}
