package lab

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// sourceBundle has a positive allowlist. Personal files, Git, Terraform, secrets,
// runtime backups outside these source roots are never part of the transport.
func sourceBundle(root string) ([]byte, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	roots := []string{"cmd/proxy", "cmd/product", "cmd/counter", "cmd/barista", "cmd/kitchen", "cmd/migrate",
		"internal/product", "internal/counter", "internal/barista", "internal/kitchen", "internal/pkg", "pkg", "proto", "third_party", "docker", "db",
		"infrastructure/iximiuz", "infrastructure/k8s/apps/coffeeshop/base",
		"infrastructure/k8s/apps/coffeeshop/overlays/iximiuz-core", "infrastructure/k8s/apps/coffeeshop/overlays/iximiuz-orders",
		"go.mod", "go.sum", ".dockerignore", "scripts/ci/build-go-service.sh",
		"platform/components.yaml", "platform/toolchain.yaml",
		"infrastructure/ansible/requirements-controller.txt",
		"infrastructure/ansible/playbooks/group_vars/all/versions.yml"}
	for _, selected := range roots {
		err := filepath.WalkDir(filepath.Join(root, selected), func(file string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("source symlink refused: %s", file)
			}
			name, err := filepath.Rel(root, file)
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			lower := strings.ToLower(entry.Name())
			if strings.Contains(lower, "secret") || strings.Contains(lower, "tfstate") || strings.HasSuffix(lower, ".env") || strings.HasSuffix(lower, ".pem") {
				return nil
			}
			// Only text source/config needed by this build profile. Do not ship
			// executables, local archives, dotfiles or arbitrary data in source dirs.
			ext := filepath.Ext(lower)
			allowed := ext == ".go" || ext == ".sql" || ext == ".yaml" || ext == ".yml" || ext == ".json" || ext == ".proto" || ext == ".lock"
			if !allowed && name != selected && !strings.HasPrefix(lower, "dockerfile-") {
				return nil
			}
			if strings.HasPrefix(lower, ".") && name != ".dockerignore" {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("nonregular source %s", name)
			}
			if info.Size() > 8<<20 || buf.Len() > 64<<20 {
				return fmt.Errorf("source bundle exceeds bound")
			}
			content, err := os.ReadFile(file)
			if err != nil {
				return err
			}
			// Public source/config must remain readable after Docker COPY into a
			// non-root image. Credentials are not part of this bundle.
			h := &tar.Header{Name: filepath.ToSlash(name), Mode: 0644, Size: int64(len(content))}
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			_, err = tw.Write(content)
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
