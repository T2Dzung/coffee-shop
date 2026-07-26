package terraform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// S3BackendConfig is the typed partial configuration used by the project's
// remote-state backends. A file is required because assume_role is a nested
// object and cannot be represented by a scalar -backend-config key/value flag.
type S3BackendConfig struct {
	Bucket      string
	Key         string
	Region      string
	KMSKeyARN   string
	RoleARN     string
	Encrypt     bool
	UseLockfile bool
}

func (c Client) InitS3(ctx context.Context, config S3BackendConfig) error {
	content, err := renderS3BackendConfig(config)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "platformctl-s3-backend-")
	if err != nil {
		return fmt.Errorf("create S3 backend config directory: %w", err)
	}
	defer os.RemoveAll(dir) // best-effort cleanup; the file contains no static credential

	path := filepath.Join(dir, "backend.hcl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write S3 backend config: %w", err)
	}
	return c.Init(ctx, "-backend-config="+path)
}

func renderS3BackendConfig(config S3BackendConfig) (string, error) {
	if config.Bucket == "" || config.Key == "" || config.Region == "" || config.KMSKeyARN == "" {
		return "", fmt.Errorf("S3 backend bucket, key, region and KMS key ARN are required")
	}
	var output strings.Builder
	fmt.Fprintf(&output, "bucket = %s\n", strconv.Quote(config.Bucket))
	fmt.Fprintf(&output, "key = %s\n", strconv.Quote(config.Key))
	fmt.Fprintf(&output, "region = %s\n", strconv.Quote(config.Region))
	fmt.Fprintf(&output, "encrypt = %t\n", config.Encrypt)
	fmt.Fprintf(&output, "kms_key_id = %s\n", strconv.Quote(config.KMSKeyARN))
	fmt.Fprintf(&output, "use_lockfile = %t\n", config.UseLockfile)
	if config.RoleARN != "" {
		fmt.Fprintf(&output, "assume_role = {\n  role_arn = %s\n}\n", strconv.Quote(config.RoleARN))
	}
	return output.String(), nil
}
