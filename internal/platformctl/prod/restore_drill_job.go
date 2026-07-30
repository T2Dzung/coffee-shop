package prod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func (o *RealRestoreDrillOperations) runJob(ctx context.Context, state RestoreDrillState, action, targetEndpoint string, source bool) (restoreDrillJobResult, error) {
	name := restoreDrillJobName(action, state.DrillID)
	manifest, err := restoreDrillJobManifest(name, action, targetEndpoint, state, source)
	if err != nil {
		return restoreDrillJobResult{}, err
	}
	if _, err := o.Base.Kube.Kubectl(ctx, nil, "delete", "job", name, "-n", restoreDrillNamespace,
		"--ignore-not-found", "--wait=true"); err != nil {
		return restoreDrillJobResult{}, err
	}
	if _, err := o.Base.Kube.Kubectl(ctx, strings.NewReader(string(manifest)), "apply", "-f", "-"); err != nil {
		return restoreDrillJobResult{}, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = o.Base.Kube.Kubectl(cleanupCtx, nil, "delete", "job", name, "-n", restoreDrillNamespace,
			"--ignore-not-found", "--wait=true")
	}()
	if err := o.wait(ctx, o.Base.Config.PollAttempts, "restore drill Job "+name, func(ctx context.Context) (bool, error) {
		complete, _ := o.Base.Kube.Kubectl(ctx, nil, "get", "job", name, "-n", restoreDrillNamespace,
			"-o", `jsonpath={.status.conditions[?(@.type=="Complete")].status}`)
		if complete == "True" {
			return true, nil
		}
		failed, _ := o.Base.Kube.Kubectl(ctx, nil, "get", "job", name, "-n", restoreDrillNamespace,
			"-o", `jsonpath={.status.conditions[?(@.type=="Failed")].status}`)
		if failed == "True" {
			logs, _ := o.Base.Kube.Kubectl(ctx, nil, "logs", "job/"+name, "-n", restoreDrillNamespace, "-c", "migrate")
			return false, fmt.Errorf("Job failed: %s", truncateRestoreDrillLog(logs))
		}
		return false, nil
	}); err != nil {
		return restoreDrillJobResult{}, err
	}
	logs, err := o.Base.Kube.Kubectl(ctx, nil, "logs", "job/"+name, "-n", restoreDrillNamespace, "-c", "migrate")
	if err != nil {
		return restoreDrillJobResult{}, err
	}
	result, err := decodeRestoreDrillJobResult(logs)
	if err != nil {
		return result, err
	}
	if result.Action != action || result.DrillID != state.DrillID {
		return result, errors.New("restore drill Job result identity does not match request")
	}
	return result, nil
}

type restoreDrillJobResult struct {
	Action      string    `json:"action"`
	DrillID     string    `json:"drill_id"`
	RestoreTime time.Time `json:"restore_time"`
	Checksum    string    `json:"checksum"`
}

type restoreDrillJob struct {
	APIVersion string                 `yaml:"apiVersion"`
	Kind       string                 `yaml:"kind"`
	Metadata   restoreDrillObjectMeta `yaml:"metadata"`
	Spec       restoreDrillJobSpec    `yaml:"spec"`
}

type restoreDrillObjectMeta struct {
	Name      string            `yaml:"name"`
	Namespace string            `yaml:"namespace,omitempty"`
	Labels    map[string]string `yaml:"labels,omitempty"`
}

type restoreDrillJobSpec struct {
	BackoffLimit          int                     `yaml:"backoffLimit"`
	ActiveDeadlineSeconds int                     `yaml:"activeDeadlineSeconds"`
	Template              restoreDrillPodTemplate `yaml:"template"`
}

type restoreDrillPodTemplate struct {
	Metadata restoreDrillObjectMeta `yaml:"metadata"`
	Spec     restoreDrillPodSpec    `yaml:"spec"`
}

type restoreDrillPodSpec struct {
	RestartPolicy string                  `yaml:"restartPolicy"`
	Containers    []restoreDrillContainer `yaml:"containers"`
}

type restoreDrillContainer struct {
	Name      string                       `yaml:"name"`
	Image     string                       `yaml:"image"`
	Resources map[string]map[string]string `yaml:"resources"`
	Env       []restoreDrillEnv            `yaml:"env"`
}

type restoreDrillEnv struct {
	Name      string                      `yaml:"name"`
	Value     string                      `yaml:"value,omitempty"`
	ValueFrom *restoreDrillEnvValueSource `yaml:"valueFrom,omitempty"`
}

type restoreDrillEnvValueSource struct {
	SecretKeyRef restoreDrillSecretKeyRef `yaml:"secretKeyRef"`
}

type restoreDrillSecretKeyRef struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

func restoreDrillJobManifest(name, action, targetEndpoint string, state RestoreDrillState, source bool) ([]byte, error) {
	if !immutableRestoreDrillImage(state.MigrationImage) {
		return nil, errors.New("restore drill Job requires immutable migration image")
	}
	env := []restoreDrillEnv{
		{Name: "MIGRATION_MODE", Value: "restore-drill"},
		{Name: "RESTORE_DRILL_ACTION", Value: action},
		{Name: "RESTORE_DRILL_ID", Value: state.DrillID},
	}
	if action == "write-a" || action == "write-b" {
		env = append(env, restoreDrillEnv{Name: "RESTORE_DRILL_PAYLOAD", Value: state.MarkerPayload})
	}
	if action == "validate" {
		env = append(env, restoreDrillEnv{Name: "RESTORE_DRILL_EXPECTED_CHECKSUM", Value: state.MarkerChecksum})
	}
	if source {
		env = append(env, restoreDrillEnv{Name: "PG_URL", ValueFrom: secretEnv("coffeeshop-secret", "PG_URL")})
	} else {
		if targetEndpoint == "" || state.SourcePort < 1 {
			return nil, errors.New("target restore drill Job requires endpoint and port")
		}
		env = append(env,
			restoreDrillEnv{Name: "DB_USER", Value: restoreDrillUser},
			restoreDrillEnv{Name: "DB_PASSWORD", ValueFrom: secretEnv("coffeeshop-secret", "APP_DB_PASSWORD")},
			restoreDrillEnv{Name: "DB_HOST", Value: targetEndpoint},
			restoreDrillEnv{Name: "DB_PORT", Value: strconv.Itoa(state.SourcePort)},
			restoreDrillEnv{Name: "DB_NAME", Value: restoreDrillDatabase},
		)
	}
	labels := map[string]string{"app.kubernetes.io/name": "restore-drill", "platform.coffeeshop.dev/restore-drill-id": state.DrillID}
	document := restoreDrillJob{
		APIVersion: "batch/v1", Kind: "Job",
		Metadata: restoreDrillObjectMeta{Name: name, Namespace: restoreDrillNamespace, Labels: labels},
		Spec: restoreDrillJobSpec{BackoffLimit: 0, ActiveDeadlineSeconds: 180,
			Template: restoreDrillPodTemplate{Metadata: restoreDrillObjectMeta{Labels: labels},
				Spec: restoreDrillPodSpec{RestartPolicy: "Never", Containers: []restoreDrillContainer{{
					Name: "migrate", Image: state.MigrationImage, Env: env,
					Resources: map[string]map[string]string{
						"requests": {"cpu": "50m", "memory": "64Mi"},
						"limits":   {"cpu": "250m", "memory": "128Mi"},
					},
				}}}},
		},
	}
	return yaml.Marshal(document)
}

func immutableRestoreDrillImage(image string) bool {
	parts := strings.Split(image, "@")
	return len(parts) == 2 && parts[0] != "" && len(parts[1]) == len("sha256:")+64 && digestPattern.MatchString(parts[1])
}

func secretEnv(name, key string) *restoreDrillEnvValueSource {
	return &restoreDrillEnvValueSource{SecretKeyRef: restoreDrillSecretKeyRef{Name: name, Key: key}}
}

func restoreDrillJobName(action, drillID string) string {
	action = strings.ReplaceAll(action, "_", "-")
	return "restore-drill-" + action + "-" + drillID
}

func decodeRestoreDrillJobResult(logs string) (restoreDrillJobResult, error) {
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		var result restoreDrillJobResult
		if json.Unmarshal([]byte(strings.TrimSpace(lines[index])), &result) == nil && result.Action != "" {
			return result, nil
		}
	}
	return restoreDrillJobResult{}, errors.New("restore drill Job logs contain no structured result")
}

func truncateRestoreDrillLog(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 500 {
		return value[len(value)-500:]
	}
	return value
}
