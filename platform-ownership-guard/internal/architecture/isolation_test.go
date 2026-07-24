package architecture_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductModuleDoesNotImportMechanicsLab(t *testing.T) {
	root := filepath.Join("..", "..")
	forbidden := []byte("platform-" + "operator")

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			base := entry.Name()
			if base == "bin" || base == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		if path == filepath.Join("..", "..", "internal", "architecture", "isolation_test.go") {
			return nil
		}
		if entry.Name() != "go.mod" && !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}

		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(contents, forbidden) {
			t.Errorf("mechanics lab dependency found in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk product module: %v", err)
	}
}
