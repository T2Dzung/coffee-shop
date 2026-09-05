package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func (e Engine) setup(ctx context.Context) error {
	// Installation can download toolchains/build images; the overall CLI context
	// still bounds it. Resume/status retain their shorter adapter timeout.
	e.Client.RemoteTimeout = 30 * time.Minute
	env, err := e.Client.Remote(ctx, "env", "-0")
	if err != nil {
		return err
	}
	for _, v := range strings.Split(env, "\x00") {
		k, val, _ := strings.Cut(v, "=")
		if strings.HasPrefix(k, "AWS_") && val != "" {
			return fmt.Errorf("AWS environment found in setup target")
		}
	}
	// setup is explicit, but it must not overwrite a completed lab's image lock.
	if _, err = e.Client.Remote(ctx, "test", "!", "-e", path.Join(e.Config.Workspace, ".core-installed")); err != nil {
		return fmt.Errorf("core already installed; use resume: %w", err)
	}
	if _, err = e.Client.Remote(ctx, "test", "!", "-e", path.Join(e.Config.Workspace, "runtime-core"), "-o", "-e", path.Join(e.Config.Workspace, ".setup-in-progress")); err != nil {
		return fmt.Errorf("existing unmanaged runtime lock; use resume, not setup: %w", err)
	}
	bundle, err := sourceBundle(e.Config.Root)
	if err != nil {
		return err
	}
	if _, err = e.Client.Remote(ctx, "mkdir", "-p", e.Config.Workspace); err != nil {
		return err
	}
	if _, err = e.Client.Remote(ctx, "touch", path.Join(e.Config.Workspace, ".setup-in-progress")); err != nil {
		return err
	}
	if _, err = e.Client.RemoteInput(ctx, bytes.NewReader(bundle), "tar", "-xf", "-", "--no-same-owner", "-C", e.Config.Workspace); err != nil {
		return err
	}
	inventory, err := e.setupInventory()
	if err != nil {
		return err
	}
	if _, err = e.Client.RemoteInput(ctx, bytes.NewReader(inventory), "tee", path.Join(e.Config.Workspace, "inventory.yaml")); err != nil {
		return err
	}
	commands := [][]string{
		{"sudo", "apt-get", "update", "-qq"},
		{"sudo", "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", "python3-venv"},
		{"python3", "-m", "venv", path.Join(e.Config.Workspace, ".venv")},
		{path.Join(e.Config.Workspace, ".venv/bin/pip"), "install", "-r", path.Join(e.Config.Workspace, "infrastructure/ansible/requirements-controller.txt")},
		{path.Join(e.Config.Workspace, ".venv/bin/ansible-playbook"), "-i", path.Join(e.Config.Workspace, "inventory.yaml"), path.Join(e.Config.Workspace, "infrastructure/iximiuz/bootstrap.yaml"), "--extra-vars", string(mustWorkspaceJSON(e.Config.Workspace))},
	}
	for i, args := range commands {
		if err = e.step(fmt.Sprintf("bootstrap-%d", i+1), func() error { _, err := e.Client.Remote(ctx, args...); return err }); err != nil {
			return err
		}
	}
	if err = e.preflight(ctx); err != nil {
		return err
	}
	if err = e.buildProfile(ctx, e.Config.Components, e.Config.ComponentImages, "runtime-core", "iximiuz-core"); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"kubectl", "apply", "-k", path.Join(e.Config.Workspace, "runtime-core")},
		{"kubectl", "apply", "-f", path.Join(e.Config.Workspace, "infrastructure/iximiuz/gateway.yaml")},
		{"kubectl", "apply", "-f", path.Join(e.Config.Workspace, "infrastructure/iximiuz/smoke-client.yaml")},
	} {
		if _, err = e.Client.Remote(ctx, args...); err != nil {
			return err
		}
	}
	return nil
}

func (e Engine) buildProfile(ctx context.Context, components []string, componentImages map[string]Image, runtimeDir, overlay string) error {
	var tools struct {
		Lab struct {
			GoBuilder string `yaml:"goBuilder"`
		} `yaml:"lab"`
	}
	if err := readYAML(e.Config.Root, "platform/toolchain.yaml", &tools); err != nil {
		return err
	}
	if !strings.HasPrefix(tools.Lab.GoBuilder, "golang:") || !strings.Contains(tools.Lab.GoBuilder, "@sha256:") {
		return fmt.Errorf("lab Go builder must be digest pinned")
	}
	var images []Image
	for _, name := range components {
		if err := e.step("build-"+name, func() error {
			_, err := e.Client.Remote(ctx, "docker", "run", "--rm", "-v", e.Config.Workspace+":/workspace", "-v", "coffeeshop-go-mod:/go/pkg/mod", "-v", "coffeeshop-go-build:/root/.cache/go-build", "-w", "/workspace", "-e", "GOMAXPROCS=2", "-e", "GOGC=50", tools.Lab.GoBuilder, "bash", "scripts/ci/build-go-service.sh", name)
			return err
		}); err != nil {
			return err
		}
		image := componentImages[name]
		if image.NewName == "" {
			return fmt.Errorf("missing image metadata: %s", name)
		}
		tag := image.NewName + ":" + image.NewTag
		if _, err := e.Client.Remote(ctx, "docker", "build", "-f", path.Join(e.Config.Workspace, e.Config.Dockerfiles[name]), "-t", tag, e.Config.Workspace); err != nil {
			return err
		}
		if _, err := e.Client.Remote(ctx, "docker", "push", tag); err != nil {
			return err
		}
		raw, err := e.Client.Remote(ctx, "docker", "image", "inspect", tag, "--format", "{{json .RepoDigests}}")
		if err != nil {
			return err
		}
		var digests []string
		if err = json.Unmarshal([]byte(raw), &digests); err != nil {
			return err
		}
		for _, d := range digests {
			if strings.HasPrefix(d, image.NewName+"@sha256:") {
				image.Digest = strings.TrimPrefix(d, image.NewName+"@")
			}
		}
		if image.Digest == "" {
			return fmt.Errorf("push produced no digest for %s", name)
		}
		images = append(images, Image{Name: image.NewName, NewName: image.NewName, Digest: image.Digest})
	}
	lock := struct {
		APIVersion string   `yaml:"apiVersion"`
		Kind       string   `yaml:"kind"`
		Resources  []string `yaml:"resources"`
		Images     []Image  `yaml:"images"`
	}{"kustomize.config.k8s.io/v1beta1", "Kustomization", []string{"../infrastructure/k8s/apps/coffeeshop/overlays/" + overlay}, images}
	data, err := yaml.Marshal(lock)
	if err != nil {
		return err
	}
	if _, err = e.Client.Remote(ctx, "mkdir", "-p", path.Join(e.Config.Workspace, runtimeDir)); err != nil {
		return err
	}
	_, err = e.Client.RemoteInput(ctx, bytes.NewReader(data), "tee", path.Join(e.Config.Workspace, runtimeDir, "kustomization.yaml"))
	return err
}

func mustWorkspaceJSON(workspace string) []byte {
	data, _ := json.Marshal(map[string]string{"lab_workspace": workspace})
	return data
}

func (e Engine) setupInventory() ([]byte, error) {
	var manifest struct {
		Playground struct {
			Machines []struct {
				Name    string `yaml:"name"`
				Network struct {
					Interfaces []struct {
						Address string `yaml:"address"`
					} `yaml:"interfaces"`
				} `yaml:"network"`
			} `yaml:"machines"`
		} `yaml:"playground"`
	}
	if err := readYAML(e.Config.Root, "infrastructure/iximiuz/core-playground.yaml", &manifest); err != nil {
		return nil, err
	}
	hosts := map[string]any{}
	for _, m := range manifest.Playground.Machines {
		if len(m.Network.Interfaces) != 1 {
			return nil, fmt.Errorf("one interface per core VM required")
		}
		hosts[m.Name] = map[string]any{"ansible_host": strings.Split(m.Network.Interfaces[0].Address, "/")[0]}
	}
	if len(hosts) != 4 || hosts["cplane-01"] == nil || hosts["node-01"] == nil || hosts["node-02"] == nil || e.Config.Machine != "dev-machine" {
		return nil, fmt.Errorf("unexpected core topology")
	}
	hosts[e.Config.Machine] = map[string]any{"ansible_connection": "local"}
	return yaml.Marshal(map[string]any{"all": map[string]any{"hosts": hosts, "vars": map[string]any{"ansible_user": "laborant", "ansible_ssh_common_args": "-o StrictHostKeyChecking=accept-new", "ansible_python_interpreter": "/usr/bin/python3", "lab_builder": e.Config.Machine, "lab_api": e.Config.APIServer}, "children": map[string]any{"k3s": map[string]any{"hosts": map[string]any{"cplane-01": nil, "node-01": nil, "node-02": nil}}}}})
}
