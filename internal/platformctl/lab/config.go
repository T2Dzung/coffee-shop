package lab

import (
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/component"
	"gopkg.in/yaml.v3"
)

const registry = "registry.iximiuz.com"

type Options struct {
	RunID, Playground, Binary, Machine, Workspace string
	Lifetime                                      time.Duration
	Create                                        bool
}

type Image struct {
	Name    string `yaml:"name"`
	NewName string `yaml:"newName"`
	NewTag  string `yaml:"newTag,omitempty"`
	Digest  string `yaml:"digest,omitempty"`
}

type Config struct {
	Options
	Namespace, APIServer, MenuURL, Gateway string
	Images                                 map[string]Image
	Components                             []string
	OrderImages                            map[string]Image
	OrderComponents                        []string
	Root                                   string
	Dockerfiles                            map[string]string
	ComponentImages                        map[string]Image
	OrderComponentImages                   map[string]Image
}

func Load(root string, options Options) (Config, error) {
	cfg := Config{Options: options, Root: root, Images: map[string]Image{}, OrderImages: map[string]Image{}, Dockerfiles: map[string]string{}, ComponentImages: map[string]Image{}, OrderComponentImages: map[string]Image{}}
	if !(options.Create && options.RunID == "") && !regexp.MustCompile(`^[a-f0-9]{24}$`).MatchString(options.RunID) {
		return cfg, fmt.Errorf("--run must be an explicit playground run ID")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]+$`).MatchString(options.Playground) {
		return cfg, fmt.Errorf("--playground must be the exact template name")
	}
	if options.Binary == "" || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`).MatchString(options.Machine) {
		return cfg, fmt.Errorf("labctl binary and machine are required")
	}
	if !path.IsAbs(options.Workspace) || path.Clean(options.Workspace) != options.Workspace || !strings.HasPrefix(options.Workspace, "/home/") {
		return cfg, fmt.Errorf("workspace must be a clean absolute path under /home/")
	}
	if options.Lifetime < 5*time.Minute || options.Lifetime > 3*time.Hour {
		return cfg, fmt.Errorf("lab lifetime must be between 5m and 3h")
	}
	var manifest struct {
		Name string `yaml:"name"`
	}
	if err := readYAML(root, "infrastructure/iximiuz/core-playground.yaml", &manifest); err != nil {
		return cfg, err
	}
	if manifest.Name == "" || !strings.HasPrefix(options.Playground, manifest.Name+"-") {
		return cfg, fmt.Errorf("playground must belong to the %s core template", manifest.Name)
	}
	var cilium struct {
		Host string `yaml:"k8sServiceHost"`
		Port int    `yaml:"k8sServicePort"`
	}
	if err := readYAML(root, "infrastructure/iximiuz/cilium-values.yaml", &cilium); err != nil {
		return cfg, err
	}
	if ip := net.ParseIP(cilium.Host); ip == nil || !ip.IsPrivate() || cilium.Port < 1 || cilium.Port > 65535 {
		return cfg, fmt.Errorf("lab API must be a private IP and valid port")
	}
	cfg.APIServer = "https://" + net.JoinHostPort(cilium.Host, strconv.Itoa(cilium.Port))
	var gateway struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name      string `yaml:"name"`
			Namespace string `yaml:"namespace"`
		} `yaml:"metadata"`
		Spec struct {
			Listeners []struct {
				Port     int    `yaml:"port"`
				Protocol string `yaml:"protocol"`
			} `yaml:"listeners"`
		} `yaml:"spec"`
	}
	if err := readYAML(root, "infrastructure/iximiuz/gateway.yaml", &gateway); err != nil {
		return cfg, err
	}
	if gateway.Kind != "Gateway" || len(gateway.Spec.Listeners) != 1 || gateway.Spec.Listeners[0].Protocol != "HTTP" {
		return cfg, fmt.Errorf("core requires one HTTP Gateway listener")
	}
	port := gateway.Spec.Listeners[0].Port
	if port < 1 || port > 65535 {
		return cfg, fmt.Errorf("invalid gateway port")
	}
	cfg.MenuURL = "http://" + net.JoinHostPort(cilium.Host, strconv.Itoa(port)) + "/api/v1/api/item-types"
	cfg.Gateway = gateway.Metadata.Name
	var overlay struct {
		Namespace string  `yaml:"namespace"`
		Images    []Image `yaml:"images"`
	}
	if err := readYAML(root, "infrastructure/k8s/apps/coffeeshop/overlays/iximiuz-core/kustomization.yaml", &overlay); err != nil {
		return cfg, err
	}
	if overlay.Namespace != "coffeeshop-lab" || gateway.Metadata.Namespace != overlay.Namespace || len(overlay.Images) == 0 {
		return cfg, fmt.Errorf("invalid isolated core namespace or empty image list")
	}
	cfg.Namespace = overlay.Namespace
	catalog, err := component.Load(filepath.Join(root, "platform/components.yaml"))
	if err != nil {
		return cfg, err
	}
	for _, image := range overlay.Images {
		var name string
		for _, entry := range catalog.Components {
			if entry.KustomizeImage == image.Name && entry.Kind == "service" && image.NewName == registry+"/"+entry.ImageRepository {
				name = entry.Name
				cfg.Dockerfiles[name] = entry.Dockerfile
			}
		}
		if name == "" || !regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`).MatchString(image.NewTag) {
			return cfg, fmt.Errorf("lab image must match component metadata and have a cache tag")
		}
		if _, exists := cfg.Images[image.NewName]; exists {
			return cfg, fmt.Errorf("duplicate lab image")
		}
		cfg.Images[image.NewName] = image
		cfg.ComponentImages[name] = image
		cfg.Components = append(cfg.Components, name)
	}
	var orders struct {
		Namespace string  `yaml:"namespace"`
		Images    []Image `yaml:"images"`
	}
	if err := readYAML(root, "infrastructure/k8s/apps/coffeeshop/overlays/iximiuz-orders/kustomization.yaml", &orders); err != nil {
		return cfg, err
	}
	expected := map[string]bool{"counter": true, "barista": true, "kitchen": true, "migrate": true}
	if orders.Namespace != dataNamespace || len(orders.Images) != len(expected) {
		return cfg, fmt.Errorf("invalid orders overlay namespace or image set")
	}
	for _, image := range orders.Images {
		name := ""
		for _, entry := range catalog.Components {
			if entry.KustomizeImage == image.Name && image.NewName == registry+"/"+entry.ImageRepository && expected[entry.Name] {
				name = entry.Name
				cfg.Dockerfiles[name] = entry.Dockerfile
			}
		}
		if name == "" || cfg.OrderComponentImages[name].NewName != "" || !regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]*$`).MatchString(image.NewTag) {
			return cfg, fmt.Errorf("orders image must match the explicit component set")
		}
		cfg.OrderImages[image.NewName] = image
		cfg.OrderComponentImages[name] = image
		cfg.OrderComponents = append(cfg.OrderComponents, name)
	}
	return cfg, nil
}

func readYAML(root, name string, value any) error {
	f, err := os.Open(filepath.Join(root, name))
	if err != nil {
		return err
	}
	defer f.Close()
	if err := yaml.NewDecoder(f).Decode(value); err != nil {
		return fmt.Errorf("decode %s: %w", name, err)
	}
	return nil
}
