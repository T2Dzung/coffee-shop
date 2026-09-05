package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/evidence"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/iximiuz"
	"gopkg.in/yaml.v3"
)

type Engine struct {
	Config   Config
	Client   iximiuz.Client
	Output   io.Writer
	Evidence *evidence.Recorder
}

func (e Engine) Run(ctx context.Context, action string) (err error) {
	if action != "setup" && action != "resume" && action != "status" && action != "stop" && action != "stateful" && action != "orders" {
		return fmt.Errorf("unsupported lab action %q", action)
	}
	if e.Output == nil {
		e.Output = io.Discard
	}
	if e.Evidence == nil {
		e.Evidence = evidence.New("lab-" + action)
	}
	defer func() {
		result := "passed"
		if err != nil {
			result = "failed"
		}
		e.Evidence.Finish(result)
	}()
	created := action == "setup" && e.Config.RunID == ""
	if created {
		id, startErr := e.Client.Native(ctx, "playground", "start", e.Config.Playground, "--quiet")
		if startErr != nil {
			return startErr
		}
		id = strings.TrimSpace(id)
		if !regexp.MustCompile(`^[a-f0-9]{24}$`).MatchString(id) {
			return fmt.Errorf("invalid created run ID: inspect labctl playground list")
		}
		e.Config.RunID, e.Client.RunID = id, id
		fmt.Fprintln(e.Output, "Created playground run:", id)
	}
	var status iximiuz.Status
	err = e.step("target", func() error {
		var readErr error
		status, readErr = e.Client.Status(ctx)
		if readErr != nil {
			return readErr
		}
		if status.ID != e.Config.RunID || status.Playground != e.Config.Playground {
			return fmt.Errorf("playground identity mismatch; refusing mutation")
		}
		e.Evidence.Record(evidence.Event{Phase: "lab", Step: "identity", Status: "verified", Details: map[string]any{"run_id": status.ID, "playground": status.Playground, "initial_state": status.State}})
		return nil
	})
	if err != nil {
		return err
	}
	if action == "status" {
		// Provider state only; do not label RUNNING as application healthy.
		return json.NewEncoder(e.Output).Encode(status)
	}
	if status.State != "RUNNING" && status.State != "STOPPED" {
		return fmt.Errorf("playground is %s; wait for a stable RUNNING/STOPPED state", status.State)
	}
	if action == "stop" {
		if status.State == "STOPPED" {
			fmt.Fprintln(e.Output, "Playground already stopped.")
			return nil
		}
		if err = e.step("persist", func() error { _, err := e.Client.Native(ctx, "playground", "persist", e.Config.RunID); return err }); err != nil {
			return err
		}
		return e.step("stop-preserving-drives", func() error { _, err := e.Client.Native(ctx, "playground", "stop", e.Config.RunID); return err })
	}
	if status.State == "STOPPED" {
		if err = e.step("restart", func() error { _, err := e.Client.Native(ctx, "playground", "restart", e.Config.RunID); return err }); err != nil {
			return err
		}
	}
	if err = e.step("persist", func() error { _, err := e.Client.Native(ctx, "playground", "persist", e.Config.RunID); return err }); err != nil {
		return err
	}
	// Lifetime is counted from session start. Resetting it on an already-running
	// session could expire that session immediately; preserve its current deadline.
	if status.State == "STOPPED" || created {
		if err = e.step("timebox", func() error {
			_, err := e.Client.Native(ctx, "playground", "lifetime", e.Config.RunID, e.Config.Lifetime.String())
			return err
		}); err != nil {
			return err
		}
	}
	if action == "setup" {
		if err = e.step("clean-install", func() error { return e.setup(ctx) }); err != nil {
			return err
		}
	}
	if err = e.step("lab-boundary", func() error { return e.preflight(ctx) }); err != nil {
		return err
	}
	var images []Image
	if err = e.step("image-lock-and-cache", func() error { var readErr error; images, readErr = e.cache(ctx); return readErr }); err != nil {
		return err
	}
	if err = e.step("registry-recovery", func() error {
		return e.recoverImages(ctx, images, e.Config.Images)
	}); err != nil {
		return err
	}
	if err = e.step("workloads-ready", func() error { return e.ready(ctx) }); err != nil {
		return err
	}
	if err = e.step("menu-smoke", func() error { return e.smoke(ctx) }); err != nil {
		return err
	}
	if action == "setup" {
		if _, err = e.Client.Remote(ctx, "touch", path.Join(e.Config.Workspace, ".core-installed")); err != nil {
			return err
		}
	}
	if action == "stateful" {
		if err = e.stateful(ctx); err != nil {
			return err
		}
	} else {
		present, detectErr := e.statefulPresent(ctx)
		if detectErr != nil {
			return detectErr
		}
		if present {
			if err = e.statefulReady(ctx); err != nil {
				return err
			}
			if err = e.statefulVerifyData(ctx); err != nil {
				return err
			}
		}
	}
	if action == "orders" {
		if err = e.orders(ctx); err != nil {
			return err
		}
	} else {
		orders, detectErr := e.ordersPresent(ctx)
		if detectErr != nil {
			return detectErr
		}
		if orders {
			if err = e.ordersRecoverAndVerify(ctx); err != nil {
				return err
			}
		}
	}
	fmt.Fprintln(e.Output, "Core ready: registry digests verified, workloads ready, menu passed. No AWS actions.")
	return nil
}

func (e Engine) preflight(ctx context.Context) error {
	env, err := e.Client.Remote(ctx, "env", "-0")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(env, "\x00") {
		key, value, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "AWS_") && value != "" {
			return fmt.Errorf("AWS environment found in lab; refusing registry/workload operations")
		}
	}
	data, err := e.Client.Remote(ctx, "kubectl", "config", "view", "--minify", "-o", "json")
	if err != nil {
		return err
	}
	var kube struct {
		Clusters []struct {
			Cluster struct {
				Server string `json:"server"`
			} `json:"cluster"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(data), &kube); err != nil {
		return fmt.Errorf("decode kubeconfig: %w", err)
	}
	if len(kube.Clusters) != 1 || kube.Clusters[0].Cluster.Server != e.Config.APIServer {
		return fmt.Errorf("Kubernetes API is not the configured lab API")
	}
	// A fault exercise must be explicitly recovered, not silently removed by resume.
	fault, err := e.Client.Remote(ctx, "kubectl", "-n", e.Config.Namespace, "get", "ciliumnetworkpolicy", "lab-deny-product", "--ignore-not-found", "-o", "name")
	if err != nil {
		return err
	}
	if strings.TrimSpace(fault) != "" {
		return fmt.Errorf("lab-deny-product is active; recover the exercise before resume")
	}
	return nil
}

func (e Engine) cache(ctx context.Context) ([]Image, error) {
	return e.cacheProfile(ctx, "runtime-core", e.Config.Images)
}

func (e Engine) cacheProfile(ctx context.Context, runtimeDir string, allowed map[string]Image) ([]Image, error) {
	data, err := e.Client.Remote(ctx, "cat", path.Join(e.Config.Workspace, runtimeDir, "kustomization.yaml"))
	if err != nil {
		return nil, err
	}
	var lock struct {
		Images []Image `yaml:"images"`
	}
	if err := yaml.Unmarshal([]byte(data), &lock); err != nil {
		return nil, fmt.Errorf("decode runtime image lock: %w", err)
	}
	if len(lock.Images) != len(allowed) {
		return nil, fmt.Errorf("runtime image set does not match %s", runtimeDir)
	}
	seen := map[string]bool{}
	for _, image := range lock.Images {
		cached, known := allowed[image.NewName]
		if !known || seen[image.NewName] || image.Name != image.NewName || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(image.Digest) {
			return nil, fmt.Errorf("invalid runtime image in %s", runtimeDir)
		}
		seen[image.NewName] = true
		data, err := e.Client.Remote(ctx, "docker", "image", "inspect", image.NewName+":"+cached.NewTag, "--format", "{{json .RepoDigests}}")
		if err != nil {
			return nil, fmt.Errorf("cache missing; rebuild explicitly before resume: %w", err)
		}
		var digests []string
		if err := json.Unmarshal([]byte(data), &digests); err != nil {
			return nil, fmt.Errorf("decode cached digests: %w", err)
		}
		found := false
		for _, digest := range digests {
			if digest == image.NewName+"@"+image.Digest {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("cached tag %s does not match runtime digest; no push performed", image.NewName)
		}
	}
	return lock.Images, nil
}

func checkDigest(headers, expected string) error {
	found := false
	for _, line := range strings.Split(headers, "\n") {
		key, value, _ := strings.Cut(line, ":")
		if strings.EqualFold(strings.TrimSpace(key), "Docker-Content-Digest") {
			if strings.TrimSpace(value) != expected {
				return fmt.Errorf("registry digest mismatch")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("registry omitted Docker-Content-Digest")
	}
	return nil
}

func (e Engine) recoverImages(ctx context.Context, images []Image, allowed map[string]Image) error {
	for _, image := range images {
		configured, ok := allowed[image.NewName]
		if !ok {
			return fmt.Errorf("refusing unconfigured registry image %s", image.NewName)
		}
		tag := configured.NewTag
		if _, err := e.Client.Remote(ctx, "docker", "push", image.NewName+":"+tag); err != nil {
			return err
		}
		headers, err := e.Client.Remote(ctx, "curl", "--fail", "--silent", "--show-error", "--max-time", "20", "--head", "--header",
			"Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json",
			"https://"+registry+"/v2/"+strings.TrimPrefix(image.NewName, registry+"/")+"/manifests/"+tag)
		if err != nil {
			return err
		}
		if err := checkDigest(headers, image.Digest); err != nil {
			return fmt.Errorf("%s: %w", image.NewName, err)
		}
	}
	return nil
}

func (e Engine) step(name string, operation func() error) error {
	fmt.Fprintln(e.Output, "lab:", name)
	start := time.Now()
	err := operation()
	status := "passed"
	if err != nil {
		status = "failed"
	}
	e.Evidence.Record(evidence.Event{Phase: "lab", Step: name, Status: status, Duration: time.Since(start)})
	if err != nil {
		return fmt.Errorf("lab %s: %w", name, err)
	}
	return nil
}
